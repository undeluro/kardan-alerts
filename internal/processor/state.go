package processor

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Sliding windows for aggregate rules live in Redis sorted sets: one member
// per event ("<eventID>|<value>", scored by event time). Re-adding an event
// after a crash replay is idempotent, so totals are not double counted.
var aggAdd = redis.NewScript(`
redis.call('ZADD', KEYS[1], ARGV[1], ARGV[3])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', tonumber(ARGV[1]) - tonumber(ARGV[2]))
redis.call('PEXPIRE', KEYS[1], tonumber(ARGV[2]) + 60000)
local members = redis.call('ZRANGE', KEYS[1], 0, -1)
local sum = 0
for _, m in ipairs(members) do
  sum = sum + (tonumber(string.match(m, '|([^|]*)$')) or 0)
end
return {#members, tostring(sum)}`)

// aggPeek recomputes a window without adding (for "back below threshold").
var aggPeek = redis.NewScript(`
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', tonumber(ARGV[1]) - tonumber(ARGV[2]))
local members = redis.call('ZRANGE', KEYS[1], 0, -1)
local sum = 0
for _, m in ipairs(members) do
  sum = sum + (tonumber(string.match(m, '|([^|]*)$')) or 0)
end
return {#members, tostring(sum)}`)

func aggKey(ruleID, fp string) string { return "kardan:agg:" + ruleID + ":" + fp }

type windowTotal struct {
	Count int64
	Sum   float64
}

func parseTotal(v any) (windowTotal, error) {
	arr, ok := v.([]any)
	if !ok || len(arr) != 2 {
		return windowTotal{}, fmt.Errorf("unexpected aggregate result %v", v)
	}
	n, _ := arr[0].(int64)
	s, _ := arr[1].(string)
	f, _ := strconv.ParseFloat(s, 64)
	return windowTotal{Count: n, Sum: f}, nil
}

// peekWindow returns the current total of an aggregate window.
func peekWindow(ctx context.Context, rdb *redis.Client, ruleID, fp string, window time.Duration) (windowTotal, error) {
	v, err := aggPeek.Run(ctx, rdb, []string{aggKey(ruleID, fp)}, time.Now().UnixMilli(), window.Milliseconds()).Result()
	if err != nil {
		return windowTotal{}, err
	}
	return parseTotal(v)
}

// Absence rules remember when each sender last reported ("realMs:bizMs"),
// and which "missing" alerts are open, per rule.
func absenceKey(ruleID string) string     { return "kardan:absence:" + ruleID }
func absenceOpenKey(ruleID string) string { return "kardan:absence-open:" + ruleID }

// parseSeen splits a "realMs:bizMs" stamp.
func parseSeen(v string) (real, biz time.Time) {
	a, b, _ := strings.Cut(v, ":")
	if ms, err := strconv.ParseInt(a, 10, 64); err == nil {
		real = time.UnixMilli(ms)
	}
	if ms, err := strconv.ParseInt(b, 10, 64); err == nil {
		biz = time.UnixMilli(ms)
	}
	return real, biz
}

const noSender = "_" // field name used when an absence rule has no "by"
