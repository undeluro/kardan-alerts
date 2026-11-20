package alerting

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

const notificationCols = `n.id, n.alert_id, coalesce(n.fallback_for, ''), n.reason, n.recipient, n.channel,
	n.address, n.subject, n.body_text, n.status, n.attempts, n.next_attempt_at, n.last_error,
	n.event_received_at, n.created_at, n.sent_at`

func scanNotification(row pgx.Row, extra ...any) (Notification, error) {
	var n Notification
	dest := []any{&n.ID, &n.AlertID, &n.FallbackFor, &n.Reason, &n.Recipient, &n.Channel, &n.Address, &n.Subject,
		&n.BodyText, &n.Status, &n.Attempts, &n.NextAttemptAt, &n.LastError, &n.EventReceivedAt, &n.CreatedAt,
		&n.SentAt}
	err := row.Scan(append(dest, extra...)...)
	return n, err
}

// Delivery is a notification with its alert's headline, for the Deliveries page.
type Delivery struct {
	Notification
	AlertTitle    string `json:"alertTitle"`
	AlertTeam     string `json:"alertTeam"`
	AlertSeverity string `json:"alertSeverity"`
}

// Deliveries lists recent notifications, optionally by status and channel.
// status may be a single status, "failing" (retry+dead) or "queued".
func (s *Store) Deliveries(ctx context.Context, status, channel string, limit int) ([]Delivery, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	where, args := []string{"true"}, []any{}
	switch status {
	case "":
		where = append(where, "n.status NOT IN ('standby', 'cancelled')")
	case "failing":
		where = append(where, "n.status IN ('retry', 'dead')")
	case "queued":
		where = append(where, "n.status IN ('pending', 'sending', 'retry')")
	default:
		args = append(args, status)
		where = append(where, fmt.Sprintf("n.status = $%d", len(args)))
	}
	if channel != "" {
		args = append(args, channel)
		where = append(where, fmt.Sprintf("n.channel = $%d", len(args)))
	}
	args = append(args, limit)
	q := `SELECT ` + notificationCols + `, a.title, a.team, a.severity
		FROM notifications n JOIN alerts a ON a.id = n.alert_id
		WHERE ` + strings.Join(where, " AND ") + fmt.Sprintf(` ORDER BY n.created_at DESC, n.id LIMIT $%d`, len(args))
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var d Delivery
		n, err := scanNotification(rows, &d.AlertTitle, &d.AlertTeam, &d.AlertSeverity)
		if err != nil {
			return nil, err
		}
		d.Notification = n
		out = append(out, d)
	}
	return out, rows.Err()
}

// Resend puts a dead, waiting or parked notification back in the queue with
// a fresh attempt budget.
func (s *Store) Resend(ctx context.Context, id string) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE notifications SET status = 'pending', attempts = 0, next_attempt_at = now(), last_error = '',
		       locked_until = NULL
		WHERE id = $1 AND status IN ('dead', 'retry', 'cancelled', 'standby')`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = s.Pool.Exec(ctx, `SELECT pg_notify('kardan_notifications', '')`)
	return err
}

// ChannelHealth summarises recent deliveries per channel.
type ChannelHealth struct {
	Channel   string  `json:"channel"`
	Sent      int64   `json:"sent"`
	Queued    int64   `json:"queued"`
	Retrying  int64   `json:"retrying"`
	Dead      int64   `json:"dead"`
	P50Ms     float64 `json:"p50Ms"`
	P99Ms     float64 `json:"p99Ms"`
	LastError string  `json:"lastError,omitempty"`
}

// ChannelHealth covers the last 15 minutes plus everything still queued.
func (s *Store) ChannelHealth(ctx context.Context) ([]ChannelHealth, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT channel,
		  count(*) FILTER (WHERE status = 'sent'),
		  count(*) FILTER (WHERE status IN ('pending', 'sending')),
		  count(*) FILTER (WHERE status = 'retry'),
		  count(*) FILTER (WHERE status = 'dead'),
		  coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM sent_at - event_received_at) * 1000)
		           FILTER (WHERE status = 'sent'), 0),
		  coalesce(percentile_cont(0.99) WITHIN GROUP (ORDER BY extract(epoch FROM sent_at - event_received_at) * 1000)
		           FILTER (WHERE status = 'sent'), 0),
		  (array_agg(last_error ORDER BY next_attempt_at DESC) FILTER (WHERE last_error <> '' AND status IN ('retry', 'dead', 'pending', 'sending')))[1]
		FROM notifications
		WHERE (created_at > now() - interval '15 minutes' OR status IN ('pending', 'sending', 'retry'))
		  AND status NOT IN ('standby', 'cancelled')
		GROUP BY channel ORDER BY channel`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChannelHealth{}
	for rows.Next() {
		var h ChannelHealth
		var lastErr *string
		if err := rows.Scan(&h.Channel, &h.Sent, &h.Queued, &h.Retrying, &h.Dead, &h.P50Ms, &h.P99Ms, &lastErr); err != nil {
			return nil, err
		}
		if lastErr != nil {
			h.LastError = *lastErr
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// FindForReply locates the alert an inbound SMS refers to: by short code if
// one is given, else the most recent active alert this person was sent.
func (s *Store) FindForReply(ctx context.Context, personID, code string) (*Alert, error) {
	q := `SELECT ` + alertColumns + ` FROM alerts WHERE status <> 'resolved'
		AND EXISTS (SELECT 1 FROM notifications n WHERE n.alert_id = alerts.id AND n.recipient = $1)`
	args := []any{personID}
	if code != "" {
		args = append(args, "%"+strings.ToUpper(code))
		q += ` AND upper(id) LIKE $2`
	}
	q += ` ORDER BY last_seen DESC LIMIT 1`
	return scanAlert(s.Pool.QueryRow(ctx, q, args...))
}

// NotificationAlert returns the alert a notification belongs to and its recipient.
func (s *Store) NotificationAlert(ctx context.Context, notificationID string) (*Alert, string, error) {
	var alertID, recipient string
	err := s.Pool.QueryRow(ctx, `SELECT alert_id, recipient FROM notifications WHERE id = $1`, notificationID).
		Scan(&alertID, &recipient)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	a, err := s.Get(ctx, alertID)
	return a, recipient, err
}
