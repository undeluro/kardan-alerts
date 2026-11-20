// Package bus names the Redis pub/sub channels replicas use to tell each
// other about changes.
package bus

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// ConfigChannel carries "rules" / "schemas" change notices.
const ConfigChannel = "kardan:config"

// NotifyConfig tells every replica to reload a kind of configuration.
func NotifyConfig(ctx context.Context, rdb *redis.Client, kind string) error {
	return rdb.Publish(ctx, ConfigChannel, kind).Err()
}
