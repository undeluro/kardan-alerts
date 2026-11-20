package alerting

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is satisfied by *pgxpool.Pool and pgx.Tx.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// ResolveFingerprint closes the open alert with this fingerprint (a recovery
// event arrived). ErrNotFound if nothing is open.
func ResolveFingerprint(ctx context.Context, q Querier, fingerprint, reason string) (*Alert, error) {
	return scanAlert(q.QueryRow(ctx, `
		UPDATE alerts SET status = 'resolved', resolved_by = $2, resolved_at = now(), updated_at = now()
		WHERE fingerprint = $1 AND status <> 'resolved'
		RETURNING `+alertColumns, fingerprint, reason))
}

// ResolveSilent closes a rule's open alerts that have not seen a matching
// event for `after` (autoResolve.after).
func ResolveSilent(ctx context.Context, q Querier, ruleID string, after time.Duration, reason string) ([]*Alert, error) {
	rows, err := q.Query(ctx, `
		UPDATE alerts SET status = 'resolved', resolved_by = $3, resolved_at = now(), updated_at = now()
		WHERE rule_id = $1 AND status <> 'resolved' AND window_end IS NULL
		  AND last_seen < now() - $2::interval
		RETURNING `+alertColumns, ruleID, after.String(), reason)
	if err != nil {
		return nil, err
	}
	return collectAlerts(rows)
}

// DueWindows returns open grouped alerts whose digest window has closed.
func DueWindows(ctx context.Context, q Querier, limit int) ([]*Alert, error) {
	rows, err := q.Query(ctx, `SELECT `+alertColumns+` FROM alerts
		WHERE status <> 'resolved' AND window_end IS NOT NULL AND window_end <= now()
		ORDER BY window_end LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return collectAlerts(rows)
}

// Resolve closes one alert for the system (window closed etc.).
func ResolveByID(ctx context.Context, q Querier, id, reason string) (*Alert, error) {
	a, err := scanAlert(q.QueryRow(ctx, `
		UPDATE alerts SET status = 'resolved', resolved_by = $2, resolved_at = now(), updated_at = now()
		WHERE id = $1 AND status <> 'resolved'
		RETURNING `+alertColumns, id, reason))
	return a, err
}

func collectAlerts(rows pgx.Rows) ([]*Alert, error) {
	defer rows.Close()
	var out []*Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Paged is someone who received an alert, for "all clear" messages.
type Paged struct {
	Recipient string
	Channel   string
	Address   string
	Subject   string // as sent, already masked for the channel
}

// PagedFor lists who was successfully sent an alert by SMS, phone or email.
func PagedFor(ctx context.Context, q Querier, alertID string) ([]Paged, error) {
	rows, err := q.Query(ctx, `
		SELECT DISTINCT ON (recipient, channel) recipient, channel, address, subject
		FROM notifications
		WHERE alert_id = $1 AND kind = 'alert' AND status = 'sent' AND channel IN ('sms', 'phone', 'email')
		ORDER BY recipient, channel, created_at`, alertID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Paged
	for rows.Next() {
		var p Paged
		if err := rows.Scan(&p.Recipient, &p.Channel, &p.Address, &p.Subject); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// HeldGroup is a recipient's throttled notifications on one channel.
type HeldGroup struct {
	Recipient string
	Channel   string
	Address   string
	IDs       []string
	Subjects  []string
	AlertIDs  []string
}

// ClaimHeld atomically takes throttled notifications whose window has ended
// (marking them suppressed) and groups them per recipient and channel, so
// exactly one digest is built for them even with several replicas sweeping.
func ClaimHeld(ctx context.Context, tx pgx.Tx) ([]HeldGroup, error) {
	rows, err := tx.Query(ctx, `
		UPDATE notifications SET status = 'suppressed', last_error = 'included in a throttling digest'
		WHERE id IN (SELECT id FROM notifications WHERE status = 'held' AND next_attempt_at <= now()
		             ORDER BY created_at LIMIT 1000 FOR UPDATE SKIP LOCKED)
		RETURNING id, recipient, channel, address, subject, alert_id, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := map[string]*HeldGroup{}
	var order []string
	type row struct {
		id, recipient, channel, address, subject, alertID string
		at                                                time.Time
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.recipient, &r.channel, &r.address, &r.subject, &r.alertID, &r.at); err != nil {
			return nil, err
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
	for _, r := range all {
		k := r.recipient + "|" + r.channel + "|" + r.address
		g := groups[k]
		if g == nil {
			g = &HeldGroup{Recipient: r.recipient, Channel: r.channel, Address: r.address}
			groups[k] = g
			order = append(order, k)
		}
		g.IDs = append(g.IDs, r.id)
		g.Subjects = append(g.Subjects, r.subject)
		g.AlertIDs = append(g.AlertIDs, r.alertID)
	}
	out := make([]HeldGroup, 0, len(order))
	for _, k := range order {
		out = append(out, *groups[k])
	}
	return out, nil
}

// IsNotFound reports ErrNotFound.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// OpenWithCutoff lists unresolved alerts racing a cut-off (urgency refresh).
func OpenWithCutoff(ctx context.Context, q Querier) ([]*Alert, error) {
	rows, err := q.Query(ctx, `SELECT `+alertColumns+` FROM alerts
		WHERE status <> 'resolved' AND cutoff_at IS NOT NULL ORDER BY cutoff_at LIMIT 2000`)
	if err != nil {
		return nil, err
	}
	return collectAlerts(rows)
}

// SetImpacts writes refreshed impact scores in one statement.
func SetImpacts(ctx context.Context, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, ids []string, impacts []int) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := q.Exec(ctx, `UPDATE alerts a SET impact = v.impact
		FROM unnest($1::text[], $2::int[]) AS v(id, impact) WHERE a.id = v.id AND a.impact <> v.impact`, ids, impacts)
	return err
}

// Unacknowledged lists a rule's open (not acknowledged) alerts below a given
// escalation level, oldest first.
func Unacknowledged(ctx context.Context, q Querier, ruleID string, belowLevel int) ([]*Alert, error) {
	rows, err := q.Query(ctx, `SELECT `+alertColumns+` FROM alerts
		WHERE status = 'open' AND rule_id = $1 AND escalation_level < $2
		  AND created_at > now() - interval '1 day' ORDER BY created_at LIMIT 500`, ruleID, belowLevel)
	if err != nil {
		return nil, err
	}
	return collectAlerts(rows)
}

// MarkEscalated records that an escalation step was taken; it only succeeds
// once per level, so concurrent sweepers cannot double-escalate.
func MarkEscalated(ctx context.Context, tx pgx.Tx, id string, fromLevel int) (bool, error) {
	tag, err := tx.Exec(ctx, `UPDATE alerts SET escalation_level = $2 + 1, escalated_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'open' AND escalation_level = $2`, id, fromLevel)
	return tag.RowsAffected() == 1, err
}

// OpenByRule lists a rule's unresolved alerts.
func OpenByRule(ctx context.Context, q Querier, ruleID string) ([]*Alert, error) {
	rows, err := q.Query(ctx, `SELECT `+alertColumns+` FROM alerts
		WHERE status <> 'resolved' AND rule_id = $1 ORDER BY created_at LIMIT 2000`, ruleID)
	if err != nil {
		return nil, err
	}
	return collectAlerts(rows)
}
