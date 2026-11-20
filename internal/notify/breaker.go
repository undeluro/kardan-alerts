package notify

import (
	"context"
	"log/slog"
	"time"

	"github.com/sony/gobreaker/v2"

	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/platform/metrics"
)

// Breaking wraps a channel in a circuit breaker. After 5 consecutive
// failures the breaker opens: deliveries on that channel are deferred
// without hammering the provider (and without spending their retry budget),
// one probe is let through every OpenFor, and the backlog drains as soon as a
// probe succeeds.
type Breaking struct {
	Channel
	cb      *gobreaker.CircuitBreaker[struct{}]
	OpenFor time.Duration
}

func NewBreaking(ch Channel, openFor time.Duration, onChange func(channel, state string), log *slog.Logger) *Breaking {
	b := &Breaking{Channel: ch, OpenFor: openFor}
	b.cb = gobreaker.NewCircuitBreaker[struct{}](gobreaker.Settings{
		Name:        ch.Name(),
		MaxRequests: 1,
		Timeout:     openFor,
		ReadyToTrip: func(c gobreaker.Counts) bool { return c.ConsecutiveFailures >= 5 },
		// A rejected phone number is not a provider outage.
		IsSuccessful: func(err error) bool { return err == nil || IsPermanent(err) },
		OnStateChange: func(name string, from, to gobreaker.State) {
			log.Warn("channel circuit breaker", "channel", name, "from", from.String(), "to", to.String())
			metrics.ChannelState.WithLabelValues(name).Set(float64(to))
			if onChange != nil {
				onChange(name, to.String())
			}
		},
	})
	metrics.ChannelState.WithLabelValues(ch.Name()).Set(0)
	return b
}

func (b *Breaking) Send(ctx context.Context, n alerting.Notification) error {
	_, err := b.cb.Execute(func() (struct{}, error) { return struct{}{}, b.Channel.Send(ctx, n) })
	return err
}

func (b *Breaking) State() string { return b.cb.State().String() }

// IsOpen reports a fast-fail from an open (or saturated half-open) breaker.
func IsOpen(err error) bool {
	return err == gobreaker.ErrOpenState || err == gobreaker.ErrTooManyRequests
}
