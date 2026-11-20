// Package audit keeps a tamper-evident record of what was done and by whom:
// acknowledgements, approvals, runbook actions and configuration changes.
//
// Entries are append-only and hash-chained: each stores the SHA-256 of its
// own content plus the previous entry's hash. Editing or deleting any entry
// breaks every hash after it, which Verify reports with the first bad entry.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Genesis is the "previous hash" of the first entry.
const Genesis = "0000000000000000000000000000000000000000000000000000000000000000"

// lockKey serialises appends across replicas (pg_advisory_xact_lock).
const lockKey = 7_426_001

type Entry struct {
	Seq      int64           `json:"seq"`
	At       time.Time       `json:"at"`
	Actor    string          `json:"actor"`
	Action   string          `json:"action"`  // e.g. alert.ack, action.approved, rule.update
	Subject  string          `json:"subject"` // e.g. alert:01J..., rule:settlements, run:01J...
	Detail   json.RawMessage `json:"detail"`
	PrevHash string          `json:"prevHash"`
	Hash     string          `json:"hash"`
}

// Event is what callers record.
type Event struct {
	Actor   string
	Action  string
	Subject string
	Detail  any
}

// Hash is the SHA-256 over the entry's fields and the previous hash. Each
// field is quoted, so text cannot move across field boundaries unnoticed.
func Hash(e Entry) string {
	h := sha256.New()
	for _, part := range []string{strconv.FormatInt(e.Seq, 10), e.At.UTC().Format(time.RFC3339Nano), e.Actor,
		e.Action, e.Subject, string(e.Detail), e.PrevHash} {
		h.Write([]byte(strconv.Quote(part)))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

type Log struct {
	Pool *pgxpool.Pool
}

// Record appends an event in its own transaction.
func (l *Log) Record(ctx context.Context, ev Event) (Entry, error) {
	var out Entry
	err := pgx.BeginFunc(ctx, l.Pool, func(tx pgx.Tx) error {
		e, err := Append(ctx, tx, ev)
		out = e
		return err
	})
	return out, err
}

// Append adds an event inside the caller's transaction, so the audit entry
// commits (or rolls back) together with the change it describes.
func Append(ctx context.Context, tx pgx.Tx, ev Event) (Entry, error) {
	detail := []byte("{}")
	if ev.Detail != nil {
		b, err := json.Marshal(ev.Detail)
		if err != nil {
			return Entry{}, err
		}
		detail = b
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey); err != nil {
		return Entry{}, err
	}
	e := Entry{Seq: 1, PrevHash: Genesis, Actor: ev.Actor, Action: ev.Action, Subject: ev.Subject, Detail: detail,
		At: time.Now().UTC().Truncate(time.Microsecond)} // Postgres keeps microseconds
	var last int64
	var prev string
	err := tx.QueryRow(ctx, `SELECT seq, hash FROM audit_log ORDER BY seq DESC LIMIT 1`).Scan(&last, &prev)
	switch {
	case err == nil:
		e.Seq, e.PrevHash = last+1, prev
	case !errors.Is(err, pgx.ErrNoRows):
		return Entry{}, err
	}
	if e.Actor == "" {
		e.Actor = "unknown"
	}
	e.Hash = Hash(e)
	_, err = tx.Exec(ctx, `INSERT INTO audit_log (seq, at, actor, action, subject, detail, prev_hash, hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, e.Seq, e.At, e.Actor, e.Action, e.Subject, string(e.Detail), e.PrevHash, e.Hash)
	return e, err
}

// Filter narrows a listing.
type Filter struct {
	Subject string // exact, e.g. alert:01J...
	Prefix  string // subject prefix, e.g. "alert:"
	Limit   int
}

func (l *Log) List(ctx context.Context, f Filter) ([]Entry, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 200
	}
	rows, err := l.Pool.Query(ctx, `SELECT seq, at, actor, action, subject, detail, prev_hash, hash FROM audit_log
		WHERE ($1 = '' OR subject = $1) AND ($2 = '' OR starts_with(subject, $2))
		ORDER BY seq DESC LIMIT $3`, f.Subject, f.Prefix, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ForSubjects lists entries about any of the subjects, oldest first (alert
// timelines include the alert and its runbook steps).
func (l *Log) ForSubjects(ctx context.Context, subjects []string) ([]Entry, error) {
	rows, err := l.Pool.Query(ctx, `SELECT seq, at, actor, action, subject, detail, prev_hash, hash FROM audit_log
		WHERE subject = ANY($1) ORDER BY seq LIMIT 1000`, subjects)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scan(rows pgx.Rows) (Entry, error) {
	var e Entry
	var detail string
	err := rows.Scan(&e.Seq, &e.At, &e.Actor, &e.Action, &e.Subject, &detail, &e.PrevHash, &e.Hash)
	e.Detail = json.RawMessage(detail)
	return e, err
}

// Verification is the result of walking the whole chain.
type Verification struct {
	OK       bool   `json:"ok"`
	Entries  int64  `json:"entries"`
	Head     string `json:"head,omitempty"` // hash of the last entry: publish it to anchor the chain
	BrokenAt int64  `json:"brokenAt,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// Verify recomputes every hash and checks every link.
func (l *Log) Verify(ctx context.Context) (Verification, error) {
	rows, err := l.Pool.Query(ctx, `SELECT seq, at, actor, action, subject, detail, prev_hash, hash FROM audit_log ORDER BY seq`)
	if err != nil {
		return Verification{}, err
	}
	defer rows.Close()
	v := Verification{OK: true}
	prev, want := Genesis, int64(1)
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return v, err
		}
		v.Entries++
		v.Head = e.Hash
		if !v.OK {
			continue
		}
		switch {
		case e.Seq != want:
			v.OK, v.BrokenAt, v.Reason = false, want, fmt.Sprintf("entry %d is missing (the next entry is %d)", want, e.Seq)
		case e.PrevHash != prev:
			v.OK, v.BrokenAt, v.Reason = false, e.Seq, "the link to the previous entry does not match (an earlier entry was changed or removed)"
		case Hash(e) != e.Hash:
			v.OK, v.BrokenAt, v.Reason = false, e.Seq, "the content does not match its hash (this entry was changed)"
		}
		prev, want = e.Hash, e.Seq+1
	}
	return v, rows.Err()
}
