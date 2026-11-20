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

	BatchSize int
	Lease     time.Duration
}

func (d *Dispatcher) Run(ctx context.Context) error {
	if d.BatchSize == 0 {
		d.BatchSize = 200
	}
	if d.Lease == 0 {
		d.Lease = 30 * time.Second
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
		          n.attempts, n.event_received_at`, limit, d.Lease.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []alerting.Notification
	for rows.Next() {
		var n alerting.Notification
		if err := rows.Scan(&n.ID, &n.AlertID, &n.Recipient, &n.Channel, &n.Address, &n.Subject, &n.BodyText,
			&n.BodyHTML, &n.Attempts, &n.EventReceivedAt); err != nil {
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
		err = errors.New("no channel registered for " + n.Channel)
	} else {
		sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err = ch.Send(sendCtx, n)
		cancel()
	}
	if ctx.Err() != nil {
		return // shutting down; the lease expires and another dispatcher retries
	}
	if err == nil {
		metrics.NotificationsSent.WithLabelValues(n.Channel, "sent").Inc()
		metrics.EndToEndLatency.WithLabelValues(n.Channel).Observe(time.Since(n.EventReceivedAt).Seconds())
		if _, uerr := d.Pool.Exec(ctx, `
			UPDATE notifications SET status = 'sent', sent_at = now(), locked_until = NULL, last_error = ''
			WHERE id = $1`, n.ID); uerr != nil {
			d.Log.Error("mark sent", "id", n.ID, "err", uerr)
		}
		return
	}

	if n.Attempts >= d.MaxAttempts {
		metrics.NotificationsSent.WithLabelValues(n.Channel, "dead").Inc()
		d.Log.Error("delivery failed permanently", "id", n.ID, "channel", n.Channel, "attempts", n.Attempts, "err", err)
		_, _ = d.Pool.Exec(ctx, `
			UPDATE notifications SET status = 'dead', locked_until = NULL, last_error = $2 WHERE id = $1`,
			n.ID, err.Error())
		return
	}
	delay := Backoff(n.Attempts)
	metrics.NotificationsSent.WithLabelValues(n.Channel, "retry").Inc()
	d.Log.Warn("delivery failed, will retry", "id", n.ID, "channel", n.Channel, "attempt", n.Attempts,
		"retryIn", delay.Round(time.Millisecond), "err", err)
	_, _ = d.Pool.Exec(ctx, `
		UPDATE notifications
		SET status = 'retry', locked_until = NULL, last_error = $2, next_attempt_at = now() + $3::interval
		WHERE id = $1`, n.ID, err.Error(), delay.String())
}

// Backoff is exponential with full jitter: ~1s, 2s, 4s ... capped at 5 min.
func Backoff(attempt int) time.Duration {
	base := time.Second << min(attempt-1, 9)
	if base > 5*time.Minute {
		base = 5 * time.Minute
	}
	return base/2 + time.Duration(rand.Int64N(int64(base/2)+1))
}
