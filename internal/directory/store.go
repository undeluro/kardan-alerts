package directory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Entry is a person or team stored through the API. Stored entries are merged
// over teams.yaml: same id replaces the file entry, a new id adds one.
type Entry struct {
	Kind      string          `json:"kind"` // person | team
	ID        string          `json:"id"`
	Body      json.RawMessage `json:"body"`
	Version   int             `json:"version"`
	UpdatedBy string          `json:"updatedBy"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

const (
	KindPerson = "person"
	KindTeam   = "team"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("changed by someone else; reload and retry")
)

// Store keeps UI-edited people and teams in Postgres.
type Store struct {
	Pool *pgxpool.Pool
}

func (s *Store) List(ctx context.Context) ([]Entry, error) {
	rows, err := s.Pool.Query(ctx, `SELECT kind, id, body, version, updated_by, updated_at
		FROM directory_entries ORDER BY kind, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.Kind, &e.ID, &e.Body, &e.Version, &e.UpdatedBy, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Put creates or replaces an entry. expectVersion > 0 enables optimistic
// locking: the write fails with ErrConflict if someone saved in between.
func (s *Store) Put(ctx context.Context, e Entry, by string, expectVersion int) (*Entry, error) {
	var out Entry
	var err error
	if expectVersion > 0 {
		err = s.Pool.QueryRow(ctx, `UPDATE directory_entries
			SET body = $3, version = version + 1, updated_by = $4, updated_at = now()
			WHERE kind = $1 AND id = $2 AND version = $5
			RETURNING kind, id, body, version, updated_by, updated_at`,
			e.Kind, e.ID, e.Body, by, expectVersion).
			Scan(&out.Kind, &out.ID, &out.Body, &out.Version, &out.UpdatedBy, &out.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrConflict
		}
	} else {
		err = s.Pool.QueryRow(ctx, `INSERT INTO directory_entries (kind, id, body, updated_by)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (kind, id) DO UPDATE
			SET body = EXCLUDED.body, version = directory_entries.version + 1,
			    updated_by = EXCLUDED.updated_by, updated_at = now()
			RETURNING kind, id, body, version, updated_by, updated_at`,
			e.Kind, e.ID, e.Body, by).
			Scan(&out.Kind, &out.ID, &out.Body, &out.Version, &out.UpdatedBy, &out.UpdatedAt)
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) Delete(ctx context.Context, kind, id string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM directory_entries WHERE kind = $1 AND id = $2`, kind, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Version changes whenever any entry changes (used by the reload safety net).
func (s *Store) Version(ctx context.Context) (string, error) {
	var n int64
	var at *time.Time
	err := s.Pool.QueryRow(ctx, `SELECT count(*), max(updated_at) FROM directory_entries`).Scan(&n, &at)
	if err != nil {
		return "", err
	}
	v := fmt.Sprint(n)
	if at != nil {
		v += "@" + at.UTC().Format(time.RFC3339Nano)
	}
	return v, nil
}
