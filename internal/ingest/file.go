package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"time"

	"github.com/nxadm/tail"
	"github.com/redis/go-redis/v9"

	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/ingest/parse"
	"github.com/undeluro/kardan-alerts/internal/platform/metrics"
)

const leaseTTL = 15 * time.Second

// renewScript extends the lease only if this replica still owns it.
var renewScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0`)

// runFile tails a file or glob. A log file lives on one machine, and tailing
// it from several replicas would duplicate every line, so a Redis lease
// elects one owner per source; if it dies, another replica with access to
// the files takes over within the lease TTL.
func runFile(ctx context.Context, c SourceConfig, p parse.Parser, m *Manager, st *sourceStats, log *slog.Logger) error {
	key := "kardan:lease:source:" + c.ID
	for ctx.Err() == nil {
		ok, err := m.Redis.SetNX(ctx, key, m.InstanceID, leaseTTL).Result()
		if err != nil {
			return err
		}
		if !ok {
			st.owner.Store(false)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(5 * time.Second):
				continue
			}
		}
		st.owner.Store(true)
		log.Info("acquired file source lease", "path", c.Path)
		err = tailWhileOwner(ctx, c, p, m, st, log, key)
		st.owner.Store(false)
		if ctx.Err() != nil {
			// Release promptly so another replica can take over.
			_ = m.Redis.Eval(context.Background(),
				`if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end return 0`,
				[]string{key}, m.InstanceID).Err()
			return nil
		}
		if err != nil {
			return err
		}
		log.Warn("lost file source lease")
	}
	return nil
}

func tailWhileOwner(ctx context.Context, c SourceConfig, p parse.Parser, m *Manager, st *sourceStats, log *slog.Logger, key string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	lines := make(chan lineEvent, 4096)
	tailers := map[string]*tail.Tail{}
	defer func() {
		for _, t := range tailers {
			_ = t.Stop()
			t.Cleanup()
		}
	}()

	start := func(path string, fromEnd bool) {
		loc := &tail.SeekInfo{Offset: 0, Whence: io.SeekStart}
		if fromEnd {
			loc = &tail.SeekInfo{Offset: 0, Whence: io.SeekEnd}
		}
		t, err := tail.TailFile(path, tail.Config{Follow: true, ReOpen: true, Poll: true, Location: loc,
			CompleteLines: true, Logger: tail.DiscardingLogger})
		if err != nil {
			log.Warn("cannot tail file", "path", path, "err", err)
			return
		}
		tailers[path] = t
		log.Info("tailing", "path", path, "from", map[bool]string{true: "end", false: "beginning"}[fromEnd])
		go func() {
			for l := range t.Lines {
				if l.Err != nil || l.Text == "" {
					continue
				}
				select {
				case lines <- lineEvent{path: path, num: l.Num, text: l.Text}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	scan := func(first bool) {
		matches, err := filepath.Glob(c.Path)
		if err != nil {
			return
		}
		for _, f := range matches {
			if _, ok := tailers[f]; !ok {
				// Files present at start follow `from`; files that appear later
				// are new and read from their beginning.
				start(f, first && c.From != "beginning")
			}
		}
	}
	scan(true)
	if len(tailers) == 0 {
		log.Info("no files match yet, waiting", "path", c.Path)
	}

	rescan := time.NewTicker(2 * time.Second)
	renew := time.NewTicker(leaseTTL / 3)
	flush := time.NewTicker(50 * time.Millisecond)
	defer rescan.Stop()
	defer renew.Stop()
	defer flush.Stop()

	var batch []*event.Envelope
	send := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := appendWithRetry(ctx, m.Producer, batch, log); err != nil {
			return err
		}
		metrics.EventsIngested.WithLabelValues(c.ID).Add(float64(len(batch)))
		st.observe(len(batch))
		batch = batch[:0]
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return send()
		case l := <-lines:
			batch = append(batch, NewEnvelope(p, c.ID, []byte(l.text), map[string]string{
				"transport": "file", "file": filepath.Base(l.path), "line": strconv.Itoa(l.num)}))
			if len(batch) >= 500 {
				if err := send(); err != nil {
					return err
				}
			}
		case <-flush.C:
			if err := send(); err != nil {
				return err
			}
		case <-rescan.C:
			scan(false)
		case <-renew.C:
			n, err := renewScript.Run(ctx, m.Redis, []string{key}, m.InstanceID, leaseTTL.Milliseconds()).Int()
			if err != nil || n == 0 {
				_ = send()
				return nil // lease lost
			}
		}
	}
}

type lineEvent struct {
	path string
	num  int
	text string
}

// appendWithRetry keeps trying while the event log is unavailable: a source
// that cannot append must apply back-pressure rather than drop events.
func appendWithRetry(ctx context.Context, p *Producer, envs []*event.Envelope, log *slog.Logger) error {
	delay := 200 * time.Millisecond
	for {
		err := p.Append(ctx, envs)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			log.Warn("append failed, retrying", "events", len(envs), "retryIn", delay, "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay < 5*time.Second {
			delay *= 2
		}
	}
}
