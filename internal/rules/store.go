package rules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store keeps rules created or edited at runtime. They are merged with file
// rules; a stored rule with a file rule's id overrides it.
type Store struct {
	Pool *pgxpool.Pool
}

var (
	ErrNotFound = errors.New("rule not found")
	ErrConflict = errors.New("rule was changed by someone else; reload and retry")
)

// Stored is a rule plus its revision metadata.
type Stored struct {
	Rule      Rule      `json:"rule"`
	Version   int       `json:"version"`
	UpdatedBy string    `json:"updatedBy"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (s *Store) List(ctx context.Context) ([]Stored, error) {
	rows, err := s.Pool.Query(ctx, `SELECT body, version, updated_by, updated_at FROM rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Stored
	for rows.Next() {
		st, err := scanStored(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *st)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, id string) (*Stored, error) {
	st, err := scanStored(s.Pool.QueryRow(ctx, `SELECT body, version, updated_by, updated_at FROM rules WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return st, err
}

func scanStored(row pgx.Row) (*Stored, error) {
	var st Stored
	var body []byte
	if err := row.Scan(&body, &st.Version, &st.UpdatedBy, &st.UpdatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &st.Rule); err != nil {
		return nil, fmt.Errorf("decode stored rule: %w", err)
	}
	st.Rule.Match.Normalize()
	st.Rule.Origin = "api"
	return &st, nil
}

// Put creates or replaces a rule. If expectVersion > 0 the write only
// succeeds when the stored version still matches (optimistic locking), so two
// people editing the same rule cannot silently overwrite each other.
func (s *Store) Put(ctx context.Context, r Rule, by string, expectVersion int) (*Stored, error) {
	r.Origin = ""
	r.Overrides = ""
	body, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	var row pgx.Row
	if expectVersion > 0 {
		row = s.Pool.QueryRow(ctx, `
			UPDATE rules SET body = $2, version = version + 1, updated_by = $3, updated_at = now()
			WHERE id = $1 AND version = $4
			RETURNING body, version, updated_by, updated_at`, r.ID, body, by, expectVersion)
	} else {
		row = s.Pool.QueryRow(ctx, `
			INSERT INTO rules (id, body, updated_by) VALUES ($1, $2, $3)
			ON CONFLICT (id) DO UPDATE SET body = EXCLUDED.body, version = rules.version + 1,
			    updated_by = EXCLUDED.updated_by, updated_at = now()
			RETURNING body, version, updated_by, updated_at`, r.ID, body, by)
	}
	st, err := scanStored(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	return st, err
}

func (s *Store) Delete(ctx context.Context, id string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM rules WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Version summarises the table so replicas can detect missed change events.
func (s *Store) Version(ctx context.Context) (string, error) {
	var n int64
	var latest *time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT count(*), max(updated_at) FROM rules`).Scan(&n, &latest); err != nil {
		return "", err
	}
	if latest == nil {
		return fmt.Sprintf("%d", n), nil
	}
	return fmt.Sprintf("%d@%d", n, latest.UnixNano()), nil
}

// Merge combines file rules with stored ones; stored rules win on id clashes.
func Merge(files []Rule, stored []Stored) []Rule {
	byID := map[string]int{}
	out := make([]Rule, 0, len(files)+len(stored))
	for _, r := range files {
		byID[r.ID] = len(out)
		out = append(out, r)
	}
	for _, st := range stored {
		r := st.Rule
		r.Origin = "api"
		if i, ok := byID[r.ID]; ok {
			r.Overrides = out[i].Origin
			out[i] = r
			continue
		}
		byID[r.ID] = len(out)
		out = append(out, r)
	}
	return out
}

// ToYAML renders a rule the way it would be written in config/rules.
func ToYAML(r Rule) ([]byte, error) {
	r.Origin, r.Overrides = "", ""
	return yaml.MarshalWithOptions(r, yaml.Indent(2), yaml.IndentSequence(true), yaml.UseLiteralStyleIfMultiline(true))
}
