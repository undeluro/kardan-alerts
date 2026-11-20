package notify

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/undeluro/kardan-alerts/internal/directory"
)

// Throttler limits how many messages one person gets per channel per window
// ("no more than 5 SMS in 10 minutes"). Messages over the limit are held and
// summarised in one digest when the window ends, so nothing is lost and
// nobody's phone buzzes fifty times. Exempt severities (critical by default)
// always go through.
type Throttler struct {
	Redis  *redis.Client
	Config func() directory.Throttle
}

// Check counts a message and reports whether it must be held, and until when.
func (t *Throttler) Check(ctx context.Context, person, channel, severity string, now time.Time) (held bool, until time.Time) {
	if t == nil || t.Redis == nil {
		return false, time.Time{}
	}
	cfg := t.Config()
	limit, ok := cfg.Channels[channel]
	if !ok || limit.Max <= 0 || cfg.IsExempt(severity) {
		return false, time.Time{}
	}
	per := limit.Duration()
	start := now.UTC().Truncate(per)
	key := fmt.Sprintf("kardan:throttle:%s:%s:%d", person, channel, start.Unix())
	pipe := t.Redis.TxPipeline()
	n := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, per+time.Minute)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, time.Time{} // fail open: better a noisy page than a lost one
	}
	if n.Val() > int64(limit.Max) {
		return true, start.Add(per)
	}
	return false, time.Time{}
}
