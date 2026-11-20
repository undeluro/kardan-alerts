// Package clock provides "now" for business-time decisions (working hours,
// on-call shifts). In demo mode the clock can be shifted, shared by every
// replica through Redis, so a presenter can show "what happens at 21:00"
// without waiting. Infrastructure timestamps (latency, leases) always use
// the real time.
package clock

import (
	"context"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	key     = "kardan:clock:offset"
	channel = "kardan:clock"
)

type Clock struct {
	Redis   *redis.Client
	Enabled bool // demo mode; when false, Now() is always real time

	offset atomic.Int64 // nanoseconds
}

func (c *Clock) Now() time.Time { return time.Now().Add(c.Offset()) }

func (c *Clock) Offset() time.Duration {
	if c == nil || !c.Enabled {
		return 0
	}
	return time.Duration(c.offset.Load())
}

// Set shifts the business clock for every replica, immediately.
func (c *Clock) Set(ctx context.Context, d time.Duration) error {
	if d == 0 {
		if err := c.Redis.Del(ctx, key).Err(); err != nil {
			return err
		}
	} else if err := c.Redis.Set(ctx, key, int64(d), 0).Err(); err != nil {
		return err
	}
	c.offset.Store(int64(d))
	return c.Redis.Publish(ctx, channel, strconv.FormatInt(int64(d), 10)).Err()
}

// Run applies changes pushed by other replicas, with a periodic re-read as a
// safety net for missed messages.
func (c *Clock) Run(ctx context.Context) error {
	if !c.Enabled {
		return nil
	}
	sub := c.Redis.Subscribe(ctx, channel)
	defer sub.Close()
	msgs := sub.Channel()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	c.reload(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case m, ok := <-msgs:
			if !ok {
				return nil
			}
			if n, err := strconv.ParseInt(m.Payload, 10, 64); err == nil {
				c.offset.Store(n)
			}
		case <-t.C:
			c.reload(ctx)
		}
	}
}

func (c *Clock) reload(ctx context.Context) {
	v, err := c.Redis.Get(ctx, key).Result()
	switch {
	case err == redis.Nil:
		c.offset.Store(0)
	case err == nil:
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			c.offset.Store(n)
		}
	}
}
