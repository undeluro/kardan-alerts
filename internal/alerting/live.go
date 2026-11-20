package alerting

import (
	"context"
	"encoding/json"

	"github.com/redis/go-redis/v9"
)

// UpdatesChannel is the Redis pub/sub channel carrying alert updates. Every
// API replica subscribes, so a browser connected to any replica sees changes
// made by any processor.
const UpdatesChannel = "kardan:alerts:updates"

type Publisher struct {
	Redis *redis.Client
}

// Publish sends updates in one pipeline round trip. Live updates are
// best-effort: the database is the source of truth and clients reload it on
// reconnect, so a failed publish is logged by the caller, not retried.
func (p *Publisher) Publish(ctx context.Context, updates []Update) error {
	if len(updates) == 0 {
		return nil
	}
	pipe := p.Redis.Pipeline()
	for _, u := range updates {
		b, err := json.Marshal(u)
		if err != nil {
			return err
		}
		pipe.Publish(ctx, UpdatesChannel, b)
	}
	_, err := pipe.Exec(ctx)
	return err
}
