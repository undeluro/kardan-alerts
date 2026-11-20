package alerting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	Pool *pgxpool.Pool
}

var ErrNotFound = errors.New("not found")

const alertColumns = `id, fingerprint, rule_id, team, status, severity, color, title, body, fields,
	source_id, first_event_id, last_event_id, sample, count, first_seen, last_seen,
	acked_by, acked_at, resolved_by, resolved_at, created_at, updated_at`

func scanAlert(row pgx.Row) (*Alert, error) {
	var a Alert
	var fields []byte
	err := row.Scan(&a.ID, &a.Fingerprint, &a.RuleID, &a.Team, &a.Status, &a.Severity, &a.Color, &a.Title,
		&a.Body, &fields, &a.SourceID, &a.FirstEventID, &a.LastEventID, &a.Sample, &a.Count, &a.FirstSeen,
		&a.LastSeen, &a.AckedBy, &a.AckedAt, &a.ResolvedBy, &a.ResolvedAt, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(fields, &a.Fields); err != nil {
		return nil, fmt.Errorf("alert %s fields: %w", a.ID, err)
	}
	return &a, nil
}

// ---- write path (processor) -------------------------------------------------

// UpsertInput is one fingerprint's worth of matching events from a batch.
type UpsertInput struct {
	Alert Alert // ID is the id to use if a new row is inserted
}

// UpsertResult says whether a new alert was opened or an open one absorbed
// the events.
type UpsertResult struct {
	ID       string
	Count    int64
	LastSeen time.Time
	Inserted bool
}

// Upsert opens alerts or folds events into the open alert with the same
// fingerprint. The partial unique index makes this correct across any number
// of processor replicas. Callers sort inputs by fingerprint so concurrent
// transactions lock rows in the same order and cannot deadlock each other.
func Upsert(ctx context.Context, tx pgx.Tx, in []UpsertInput) ([]UpsertResult, error) {
	b := &pgx.Batch{}
	for _, u := range in {
		a := u.Alert
		fields, err := json.Marshal(a.Fields)
		if err != nil {
			return nil, err
		}
		if a.Fields == nil {
			fields = []byte("[]")
		}
		b.Queue(`
			INSERT INTO alerts (id, fingerprint, rule_id, team, severity, color, title, body, fields,
			                    source_id, first_event_id, last_event_id, sample, count, first_seen, last_seen)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
			ON CONFLICT (fingerprint) WHERE status <> 'resolved' DO UPDATE SET
			    count         = alerts.count + EXCLUDED.count,
			    last_seen     = GREATEST(alerts.last_seen, EXCLUDED.last_seen),
			    last_event_id = EXCLUDED.last_event_id,
			    updated_at    = now()
			RETURNING id, count, last_seen, (xmax = 0) AS inserted`,
			a.ID, a.Fingerprint, a.RuleID, a.Team, a.Severity, a.Color, a.Title, a.Body, fields,
			a.SourceID, a.FirstEventID, a.LastEventID, a.Sample, a.Count, a.FirstSeen, a.LastSeen)
	}
	br := tx.SendBatch(ctx, b)
	defer br.Close()
	out := make([]UpsertResult, len(in))
	for i := range in {
		if err := br.QueryRow().Scan(&out[i].ID, &out[i].Count, &out[i].LastSeen, &out[i].Inserted); err != nil {
			return nil, fmt.Errorf("upsert alert %s: %w", in[i].Alert.Fingerprint, err)
		}
	}
	return out, br.Close()
}

// InsertNotifications writes outbox rows and wakes dispatchers on commit.
func InsertNotifications(ctx context.Context, tx pgx.Tx, ns []Notification) error {
	if len(ns) == 0 {
		return nil
	}
	rows := make([][]any, len(ns))
	for i, n := range ns {
		rows[i] = []any{n.ID, n.AlertID, n.Recipient, n.Channel, n.Address, n.Subject, n.BodyText, n.BodyHTML,
			n.EventReceivedAt}
	}
	_, err := tx.CopyFrom(ctx, pgx.Identifier{"notifications"},
		[]string{"id", "alert_id", "recipient", "channel", "address", "subject", "body_text", "body_html",
			"event_received_at"},
		pgx.CopyFromRows(rows))
	if err != nil {
		return fmt.Errorf("insert notifications: %w", err)
	}
	_, err = tx.Exec(ctx, `SELECT pg_notify('kardan_notifications', '')`)
	return err
}

// IsRetryable reports whether a transaction failure is transient.
func IsRetryable(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "40001", "40P01", "55P03", "57P01", "53300": // serialization, deadlock, lock timeout, admin shutdown, too many connections
			return true
		}
		return false
	}
	return true // network and pool errors
}

// ---- read / state path (API) --------------------------------------------------

type ListFilter struct {
	Team   string
	Status string // open | acked | resolved | active (open+acked) | "" (all)
	Limit  int
}

func (s *Store) List(ctx context.Context, f ListFilter) ([]*Alert, error) {
	var where []string
	var args []any
	if f.Team != "" {
		args = append(args, f.Team)
		where = append(where, fmt.Sprintf("team = $%d", len(args)))
	}
	switch f.Status {
	case "":
	case "active":
		where = append(where, "status <> 'resolved'")
	default:
		args = append(args, f.Status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 200
	}
	q := `SELECT ` + alertColumns + ` FROM alerts`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, f.Limit)
	q += fmt.Sprintf(" ORDER BY last_seen DESC LIMIT $%d", len(args))

	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Alert{}
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, id string) (*Alert, error) {
	return scanAlert(s.Pool.QueryRow(ctx, `SELECT `+alertColumns+` FROM alerts WHERE id = $1`, id))
}

// Ack marks an open alert as being worked on.
func (s *Store) Ack(ctx context.Context, id, by string) (*Alert, error) {
	a, err := scanAlert(s.Pool.QueryRow(ctx, `
		UPDATE alerts SET status = 'acked', acked_by = $2, acked_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'open'
		RETURNING `+alertColumns, id, by))
	if errors.Is(err, ErrNotFound) {
		return s.Get(ctx, id) // already acked/resolved: idempotent
	}
	return a, err
}

// Resolve closes an alert. Its fingerprint is freed, so the next matching
// event opens a fresh alert.
func (s *Store) Resolve(ctx context.Context, id, by string) (*Alert, error) {
	a, err := scanAlert(s.Pool.QueryRow(ctx, `
		UPDATE alerts SET status = 'resolved', resolved_by = $2, resolved_at = now(), updated_at = now()
		WHERE id = $1 AND status <> 'resolved'
		RETURNING `+alertColumns, id, by))
	if errors.Is(err, ErrNotFound) {
		return s.Get(ctx, id)
	}
	return a, err
}

func (s *Store) Notifications(ctx context.Context, alertID string) ([]Notification, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, alert_id, recipient, channel, address, subject, body_text, status, attempts,
		       next_attempt_at, last_error, event_received_at, created_at, sent_at
		FROM notifications WHERE alert_id = $1 ORDER BY created_at, id`, alertID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.AlertID, &n.Recipient, &n.Channel, &n.Address, &n.Subject, &n.BodyText,
			&n.Status, &n.Attempts, &n.NextAttemptAt, &n.LastError, &n.EventReceivedAt, &n.CreatedAt,
			&n.SentAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Stats summarises alert and delivery state for the dashboard header.
type Stats struct {
	OpenAlerts        int64 `json:"openAlerts"`
	AckedAlerts       int64 `json:"ackedAlerts"`
	PendingDeliveries int64 `json:"pendingDeliveries"`
	FailedDeliveries  int64 `json:"failedDeliveries"`
	EventsLastHour    int64 `json:"eventsLastHour"`
}

func (s *Store) Stats(ctx context.Context, team string) (Stats, error) {
	var st Stats
	err := s.Pool.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE status = 'open'),
		  count(*) FILTER (WHERE status = 'acked'),
		  coalesce(sum(count) FILTER (WHERE last_seen > now() - interval '1 hour'), 0)
		FROM alerts WHERE ($1 = '' OR team = $1)`, team).Scan(&st.OpenAlerts, &st.AckedAlerts, &st.EventsLastHour)
	if err != nil {
		return st, err
	}
	err = s.Pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE n.status IN ('pending', 'retry', 'sending')),
		       count(*) FILTER (WHERE n.status = 'dead')
		FROM notifications n JOIN alerts a ON a.id = n.alert_id
		WHERE ($1 = '' OR a.team = $1)`, team).Scan(&st.PendingDeliveries, &st.FailedDeliveries)
	return st, err
}
