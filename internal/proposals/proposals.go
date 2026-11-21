// Package proposals is the review queue for changes to rules and runbooks:
// anyone (or Kardan's fatigue analysis, or an AI draft) proposes, a different
// person accepts or rejects, like a pull request.
package proposals

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Proposal struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"` // rule | runbook
	Target       string     `json:"target"`
	Title        string     `json:"title"`
	Rationale    string     `json:"rationale"`
	Body         string     `json:"body"`
	BaseVersion  int        `json:"baseVersion"`
	Source       string     `json:"source"` // person | system | ai
	Signal       string     `json:"signal,omitempty"`
	AlertID      string     `json:"alertId,omitempty"`
	Status       string     `json:"status"`
	CreatedBy    string     `json:"createdBy"`
	CreatedByID  string     `json:"createdById,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	DecidedBy    string     `json:"decidedBy,omitempty"`
	DecidedAt    *time.Time `json:"decidedAt,omitempty"`
	DecisionNote string     `json:"decisionNote,omitempty"`
}

var (
	ErrNotFound = errors.New("proposal not found")
	ErrDecided  = errors.New("proposal was already decided")
	ErrExists   = errors.New("an open suggestion for this already exists")
)

type Store struct{ Pool *pgxpool.Pool }

const cols = `id, kind, target, title, rationale, body, base_version, source, signal, coalesce(alert_id, ''), status,
	created_by, created_by_id, created_at, coalesce(decided_by, ''), decided_at, decision_note`

func scan(row pgx.Row) (*Proposal, error) {
	var p Proposal
	err := row.Scan(&p.ID, &p.Kind, &p.Target, &p.Title, &p.Rationale, &p.Body, &p.BaseVersion, &p.Source, &p.Signal,
		&p.AlertID, &p.Status, &p.CreatedBy, &p.CreatedByID, &p.CreatedAt, &p.DecidedBy, &p.DecidedAt, &p.DecisionNote)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &p, err
}

func (s *Store) Create(ctx context.Context, p Proposal) (*Proposal, error) {
	var alert *string
	if p.AlertID != "" {
		alert = &p.AlertID
	}
	out, err := scan(s.Pool.QueryRow(ctx, `INSERT INTO proposals
		(id, kind, target, title, rationale, body, base_version, source, signal, alert_id, created_by, created_by_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (kind, target, signal) WHERE status = 'open' AND signal <> '' DO NOTHING
		RETURNING `+cols, p.ID, p.Kind, p.Target, p.Title, p.Rationale, p.Body, p.BaseVersion, p.Source, p.Signal, alert, p.CreatedBy, p.CreatedByID))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrExists
	}
	return out, err
}

func (s *Store) Get(ctx context.Context, id string) (*Proposal, error) {
	return scan(s.Pool.QueryRow(ctx, `SELECT `+cols+` FROM proposals WHERE id = $1`, id))
}

func (s *Store) List(ctx context.Context, status string, limit int) ([]*Proposal, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+cols+` FROM proposals
		WHERE ($1 = '' OR status = $1) ORDER BY (status = 'open') DESC, created_at DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Proposal
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Decide records the decision; only open proposals can be decided, so two
// reviewers clicking at once cannot both apply it.
func (s *Store) Decide(ctx context.Context, id, status, by, note string) (*Proposal, error) {
	p, err := scan(s.Pool.QueryRow(ctx, `UPDATE proposals
		SET status = $2, decided_by = $3, decided_at = now(), decision_note = $4
		WHERE id = $1 AND status = 'open' RETURNING `+cols, id, status, by, note))
	if errors.Is(err, ErrNotFound) {
		if _, gerr := s.Get(ctx, id); gerr == nil {
			return nil, ErrDecided
		}
	}
	return p, err
}

// Reopen undoes a decision whose change could not be applied.
func (s *Store) Reopen(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE proposals SET status = 'open', decided_by = NULL, decided_at = NULL, decision_note = ''
		WHERE id = $1`, id)
	return err
}
