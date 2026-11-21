package notify

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/platform/metrics"
)

// Dispatcher drains the notification outbox. Any number of dispatcher
// replicas can run: rows are claimed with FOR UPDATE SKIP LOCKED and leased
// with locked_until, so a crashed dispatcher's rows are picked up again once
// the lease expires. A failing channel only delays its own rows.
type Dispatcher struct {
	Pool        *pgxpool.Pool
	Channels    map[string]Channel
	Workers     int
	MaxAttempts int
	Log         *slog.Logger

	BatchSize     int
	Lease         time.Duration
	OpenFor       time.Duration // how long to wait when a channel's breaker is open
	FallbackAfter int           // failed attempts before the fallback channel is used
	FallbackDelay time.Duration // or: time undelivered before the fallback is used
}

func (d *Dispatcher) Run(ctx context.Context) error {
	if d.BatchSize == 0 {
		d.BatchSize = 200
	}
	if d.Lease == 0 {
		d.Lease = 30 * time.Second
	}
	if d.OpenFor == 0 {
		d.OpenFor = 15 * time.Second
	}
	if d.FallbackAfter == 0 {
		d.FallbackAfter = 3
	}
	if d.FallbackDelay == 0 {
		d.FallbackDelay = 30 * time.Second
	}
	wake := make(chan struct{}, 1)
	go d.listen(ctx, wake)

	// slots bounds in-flight deliveries. Rows are claimed only when a worker
	// is free to start them right away, so a lease never expires while a row
	// sits in a local queue (which would let another replica send it twice).
	slots := make(chan struct{}, d.Workers)
	freed := make(chan struct{}, 1)
	var wg sync.WaitGroup
	defer wg.Wait()

	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	d.Log.Info("dispatcher started", "workers", d.Workers)
	for {
		for {
			free := d.Workers - len(slots)
			if free == 0 {
				break
			}
			batch, err := d.claim(ctx, min(free, d.BatchSize))
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				d.Log.Error("claim notifications", "err", err)
				break
			}
			for _, n := range batch {
				slots <- struct{}{}
				wg.Add(1)
				go func(n alerting.Notification) {
					defer wg.Done()
					d.deliver(ctx, n)
					<-slots
					select {
					case freed <- struct{}{}:
					default:
					}
				}(n)
			}
			if len(batch) < free {
				break // nothing more due right now
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-wake:
		case <-freed:
		case <-tick.C:
		}
	}
}

// listen turns Postgres NOTIFY into wake-ups so new notifications go out
// within milliseconds instead of on the next poll.
func (d *Dispatcher) listen(ctx context.Context, wake chan<- struct{}) {
	for ctx.Err() == nil {
		conn, err := d.Pool.Acquire(ctx)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		if _, err := conn.Exec(ctx, "LISTEN kardan_notifications"); err != nil {
			conn.Release()
			time.Sleep(time.Second)
			continue
		}
		for {
			if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
				break
			}
			select {
			case wake <- struct{}{}:
			default:
			}
		}
		conn.Release()
	}
}

func (d *Dispatcher) claim(ctx context.Context, limit int) ([]alerting.Notification, error) {
	rows, err := d.Pool.Query(ctx, `
		UPDATE notifications n
		SET status = 'sending', attempts = n.attempts + 1, locked_until = now() + $2::interval
		WHERE n.id IN (
		    SELECT id FROM notifications
		    WHERE ((status IN ('pending', 'retry') AND next_attempt_at <= now())
		        OR (status = 'sending' AND locked_until < now()))
		    ORDER BY next_attempt_at
		    LIMIT $1
		    FOR UPDATE SKIP LOCKED)
		RETURNING n.id, n.alert_id, n.recipient, n.channel, n.address, n.subject, n.body_text, n.body_html,
		          n.attempts, n.event_received_at, n.created_at, n.kind`, limit, d.Lease.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []alerting.Notification
	for rows.Next() {
		var n alerting.Notification
		if err := rows.Scan(&n.ID, &n.AlertID, &n.Recipient, &n.Channel, &n.Address, &n.Subject, &n.BodyText,
			&n.BodyHTML, &n.Attempts, &n.EventReceivedAt, &n.CreatedAt, &n.Kind); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (d *Dispatcher) deliver(ctx context.Context, n alerting.Notification) {
	ch, ok := d.Channels[n.Channel]
	var err error
	if !ok {
		err = &PermanentError{errors.New("no channel registered for " + n.Channel)}
	} else {
		sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err = ch.Send(sendCtx, n)
		cancel()
	}
	if ctx.Err() != nil {
		return // shutting down; the lease expires and another dispatcher retries
	}
	log := d.Log.With("id", n.ID, "channel", n.Channel, "to", n.Recipient, "attempt", n.Attempts)

	switch {
	case err == nil:
		metrics.NotificationsSent.WithLabelValues(n.Channel, "sent").Inc()
		metrics.EndToEndLatency.WithLabelValues(n.Channel).Observe(time.Since(n.EventReceivedAt).Seconds())
		// Mark it sent and cancel its parked fallbacks (no longer needed) in one
		// statement: one commit per delivery instead of two.
		d.exec(ctx, `WITH sent AS (
				UPDATE notifications SET status = 'sent', sent_at = now(), locked_until = NULL, last_error = ''
				WHERE id = $1 RETURNING id)
			UPDATE notifications SET status = 'cancelled'
			WHERE fallback_for IN (SELECT id FROM sent) AND status = 'standby'`, n.ID)

	case IsOpen(err):
		// The channel is known to be down. Wait for the breaker's next probe
		// without spending this delivery's retry budget.
		metrics.NotificationsSent.WithLabelValues(n.Channel, "deferred").Inc()
		wait := d.OpenFor
		d.exec(ctx, `UPDATE notifications SET status = 'retry', attempts = greatest(attempts - 1, 0), locked_until = NULL,
			last_error = $2, next_attempt_at = now() + $3::interval WHERE id = $1`,
			n.ID, n.Channel+" channel unavailable (circuit open), waiting for it to recover", wait.String())
		d.maybeFallback(ctx, n, log)

	case IsPermanent(err) || n.Attempts >= d.MaxAttempts:
		metrics.NotificationsSent.WithLabelValues(n.Channel, "dead").Inc()
		log.Error("delivery failed for good, moved to dead letters", "err", err)
		d.exec(ctx, `UPDATE notifications SET status = 'dead', locked_until = NULL, last_error = $2 WHERE id = $1`,
			n.ID, err.Error())
		d.activateFallback(ctx, n, log)

	default:
		delay := Backoff(n.Attempts)
		metrics.NotificationsSent.WithLabelValues(n.Channel, "retry").Inc()
		log.Warn("delivery failed, will retry", "retryIn", delay.Round(time.Millisecond), "err", err)
		d.exec(ctx, `UPDATE notifications SET status = 'retry', locked_until = NULL, last_error = $2,
			next_attempt_at = now() + $3::interval WHERE id = $1`, n.ID, err.Error(), delay.String())
		d.maybeFallback(ctx, n, log)
	}
}

// maybeFallback activates the parked fallback once the primary has failed a
// few times or has been undelivered for too long (e.g. channel outage).
func (d *Dispatcher) maybeFallback(ctx context.Context, n alerting.Notification, log *slog.Logger) {
	if n.Attempts >= d.FallbackAfter || time.Since(n.CreatedAt) >= d.FallbackDelay {
		d.activateFallback(ctx, n, log)
	}
}

func (d *Dispatcher) activateFallback(ctx context.Context, n alerting.Notification, log *slog.Logger) {
	tag, err := d.Pool.Exec(ctx, `UPDATE notifications SET status = 'pending', next_attempt_at = now()
		WHERE fallback_for = $1 AND status = 'standby'`, n.ID)
	if err != nil {
		log.Error("activate fallback", "err", err)
		return
	}
	if tag.RowsAffected() > 0 {
		metrics.NotificationsSent.WithLabelValues(n.Channel, "fallback").Inc()
		log.Warn("primary channel failing, fallback delivery activated")
		d.exec(ctx, `SELECT pg_notify('kardan_notifications', '')`)
	}
}

func (d *Dispatcher) exec(ctx context.Context, sql string, args ...any) {
	if _, err := d.Pool.Exec(ctx, sql, args...); err != nil {
		d.Log.Error("update notification", "err", err)
	}
}

// Backoff is exponential with full jitter: ~1s, 2s, 4s ... capped at 5 min.
func Backoff(attempt int) time.Duration {
	base := time.Second << min(attempt-1, 9)
	if base > 5*time.Minute {
		base = 5 * time.Minute
	}
	return base/2 + time.Duration(rand.Int64N(int64(base/2)+1))
}
