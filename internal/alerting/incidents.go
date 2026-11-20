package alerting

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/undeluro/kardan-alerts/internal/schema"
)

// Incident groups alerts that share a business key within a window:
// "Counterparty ACME — 4 alerts across settlements, rates and payments,
// $410M at risk".
type Incident struct {
	ID          string     `json:"id"`
	Key         string     `json:"key"`
	KeyField    string     `json:"keyField"`
	KeyValue    string     `json:"keyValue"`
	Status      string     `json:"status"`
	AlertCount  int        `json:"alertCount"`
	OpenAlerts  int        `json:"openAlerts"`
	Teams       []string   `json:"teams"`
	MaxSeverity string     `json:"maxSeverity"`
	NotionalUSD float64    `json:"notionalUsd"`
	Impact      int        `json:"impact"`
	FirstSeen   time.Time  `json:"firstSeen"`
	LastSeen    time.Time  `json:"lastSeen"`
	ResolvedAt  *time.Time `json:"resolvedAt,omitempty"`
}

// IncidentInput links a newly opened alert to its incident.
type IncidentInput struct {
	NewID       string // used if a new incident is created
	AlertID     string
	KeyField    string
	KeyValue    string
	Team        string
	Severity    string
	NotionalUSD float64
	Impact      int
	At          time.Time
}

// LinkIncidents adds new alerts to the open incident for their key (creating
// it if needed) inside the alert transaction. Inputs are processed in key
// order so concurrent processors lock rows consistently.
func LinkIncidents(ctx context.Context, tx pgx.Tx, in []IncidentInput) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	sort.Slice(in, func(i, j int) bool {
		return in[i].KeyField+"="+in[i].KeyValue < in[j].KeyField+"="+in[j].KeyValue
	})
	b := &pgx.Batch{}
	for _, x := range in {
		key := x.KeyField + "=" + x.KeyValue
		b.Queue(`
			INSERT INTO incidents (id, key, key_field, key_value, alert_count, teams, max_level, notional_usd, impact,
			                       first_seen, last_seen)
			VALUES ($1, $2, $3, $4, 1, ARRAY[$5::text], $6, $7, $8, $9, $9)
			ON CONFLICT (key) WHERE status = 'open' DO UPDATE SET
			    alert_count  = incidents.alert_count + 1,
			    teams        = CASE WHEN $5 = ANY(incidents.teams) THEN incidents.teams
			                        ELSE array_append(incidents.teams, $5::text) END,
			    max_level    = GREATEST(incidents.max_level, EXCLUDED.max_level),
			    notional_usd = incidents.notional_usd + EXCLUDED.notional_usd,
			    impact       = GREATEST(incidents.impact, EXCLUDED.impact),
			    last_seen    = GREATEST(incidents.last_seen, EXCLUDED.last_seen)
			RETURNING id`,
			x.NewID, key, x.KeyField, x.KeyValue, x.Team, int(schema.ParseLevel(x.Severity)), x.NotionalUSD, x.Impact, x.At)
	}
	br := tx.SendBatch(ctx, b)
	ids := make([]string, len(in))
	for i := range in {
		if err := br.QueryRow().Scan(&ids[i]); err != nil {
			br.Close()
			return nil, fmt.Errorf("link incident %s=%s: %w", in[i].KeyField, in[i].KeyValue, err)
		}
	}
	if err := br.Close(); err != nil {
		return nil, err
	}
	alertIDs := make([]string, len(in))
	links := make(map[string]string, len(in))
	for i, x := range in {
		alertIDs[i] = x.AlertID
		links[x.AlertID] = ids[i]
	}
	_, err := tx.Exec(ctx, `UPDATE alerts a SET incident_id = v.incident
		FROM unnest($1::text[], $2::text[]) AS v(alert, incident) WHERE a.id = v.alert`, alertIDs, ids)
	return links, err
}

// CloseIncidents resolves incidents whose alerts are all resolved, or that
// saw no new alert for the correlation window.
func CloseIncidents(ctx context.Context, q Querier, window time.Duration) (int, error) {
	rows, err := q.Query(ctx, `
		UPDATE incidents i SET status = 'resolved', resolved_at = now()
		WHERE i.status = 'open' AND (
		      i.last_seen < now() - $1::interval
		   OR NOT EXISTS (SELECT 1 FROM alerts a WHERE a.incident_id = i.id AND a.status <> 'resolved'))
		RETURNING i.id`, window.String())
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	return n, rows.Err()
}

const incidentCols = `i.id, i.key, i.key_field, i.key_value, i.status, i.alert_count, i.teams, i.max_level,
	i.notional_usd, i.impact, i.first_seen, i.last_seen, i.resolved_at,
	(SELECT count(*) FROM alerts a WHERE a.incident_id = i.id AND a.status <> 'resolved')`

func scanIncident(row pgx.Row) (*Incident, error) {
	var x Incident
	var level int
	err := row.Scan(&x.ID, &x.Key, &x.KeyField, &x.KeyValue, &x.Status, &x.AlertCount, &x.Teams, &level,
		&x.NotionalUSD, &x.Impact, &x.FirstSeen, &x.LastSeen, &x.ResolvedAt, &x.OpenAlerts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	x.MaxSeverity = schema.Level(level).String()
	// Several correlated alerts make an incident more urgent than any one of them.
	x.Impact = min(100, x.Impact+min(15, 3*(x.AlertCount-1)))
	return &x, nil
}

// Incidents lists incidents with at least minAlerts alerts.
func (s *Store) Incidents(ctx context.Context, status string, minAlerts int) ([]*Incident, error) {
	q := `SELECT ` + incidentCols + ` FROM incidents i WHERE i.alert_count >= $1`
	args := []any{minAlerts}
	if status != "" {
		args = append(args, status)
		q += ` AND i.status = $2`
	}
	q += ` ORDER BY i.status, i.impact DESC, i.last_seen DESC LIMIT 100`
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Incident{}
	for rows.Next() {
		x, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) Incident(ctx context.Context, id string) (*Incident, error) {
	return scanIncident(s.Pool.QueryRow(ctx, `SELECT `+incidentCols+` FROM incidents i WHERE i.id = $1`, id))
}
