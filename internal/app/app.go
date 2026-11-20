// Package app wires components together according to the replica's roles.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"
	"golang.org/x/sync/errgroup"

	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/api"
	"github.com/undeluro/kardan-alerts/internal/directory"
	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/ingest"
	"github.com/undeluro/kardan-alerts/internal/notify"
	"github.com/undeluro/kardan-alerts/internal/platform/bus"
	"github.com/undeluro/kardan-alerts/internal/platform/clock"
	"github.com/undeluro/kardan-alerts/internal/platform/config"
	"github.com/undeluro/kardan-alerts/internal/platform/kafka"
	"github.com/undeluro/kardan-alerts/internal/platform/pg"
	"github.com/undeluro/kardan-alerts/internal/processor"
	"github.com/undeluro/kardan-alerts/internal/rules"
	"github.com/undeluro/kardan-alerts/internal/schema"
	"github.com/undeluro/kardan-alerts/web"
)

var Version = "dev"

func Run(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	log.Info("starting kardan", "version", Version, "roles", cfg.RoleList(), "instance", cfg.InstanceID)

	pool, err := pg.Connect(ctx, cfg.PostgresURL, log)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pg.Migrate(ctx, pool, log); err != nil {
		return err
	}

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer rdb.Close()
	if err := waitRedis(ctx, rdb, log); err != nil {
		return err
	}

	store := &alerting.Store{Pool: pool}
	publisher := &alerting.Publisher{Redis: rdb}
	engine := rules.NewEngine()
	ruleStore := &rules.Store{Pool: pool}
	schemas := schema.NewRegistry(pool, log.With("component", "schemas"))

	if cfg.Has(config.RoleIngest) || cfg.Has(config.RoleProcessor) {
		admin, err := kafka.NewClient(ctx, cfg.KafkaBrokers, log)
		if err != nil {
			return err
		}
		err = kafka.EnsureTopic(ctx, admin, cfg.EventsTopic, cfg.EventsPartitions, log)
		admin.Close()
		if err != nil {
			return err
		}
	}

	// Ingest: producer to the events topic, plus the configured sources.
	var sources *ingest.Manager
	var producer *ingest.Producer
	if cfg.Has(config.RoleIngest) {
		prod, err := kafka.NewClient(ctx, cfg.KafkaBrokers, log,
			kgo.ProducerLinger(0),
			kgo.ProducerBatchCompression(kgo.Lz4Compression(), kgo.NoCompression()),
		)
		if err != nil {
			return err
		}
		defer prod.Close()
		producer = &ingest.Producer{Kafka: prod, Topic: cfg.EventsTopic}
		sources = &ingest.Manager{Producer: producer, Redis: rdb, DefaultBrokers: cfg.KafkaBrokers,
			InstanceID: cfg.InstanceID, Log: log.With("component", "sources")}
	}

	reloader := &Reloader{
		ConfigDir: cfg.ConfigDir, Window: cfg.DedupWindow, Engine: engine, RuleStore: ruleStore,
		Schemas: schemas, Sources: sources, Redis: rdb, Log: log.With("component", "config"),
	}
	if err := reloader.LoadAll(ctx); err != nil {
		return err
	}

	biz := &clock.Clock{Redis: rdb, Enabled: cfg.DemoMode}

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return reloader.Run(ctx) })
	g.Go(func() error { return biz.Run(ctx) })
	mux := http.NewServeMux()

	// Health and metrics exist on every replica, whatever its roles.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, "postgres: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("GET /metrics", promhttp.Handler())

	if cfg.Has(config.RoleIngest) {
		(&ingest.HTTPHandler{Producer: producer, Parsers: sources.ParserFor, Log: log}).Register(mux)
	}

	if cfg.Has(config.RoleProcessor) {
		cons, err := kafka.NewClient(ctx, cfg.KafkaBrokers, log,
			kgo.ConsumerGroup(cfg.ProcessorGroup),
			kgo.ConsumeTopics(cfg.EventsTopic),
			kgo.DisableAutoCommit(),
			kgo.BlockRebalanceOnPoll(),
			kgo.FetchMaxWait(250*time.Millisecond),
		)
		if err != nil {
			return err
		}
		defer cons.Close()
		p := &processor.Processor{
			Kafka: cons, Pool: pool, Engine: engine, Directory: reloader.Directory, Privacy: reloader.Privacy,
			FX: reloader.FX, Correlation: reloader.Correlation, Calendars: reloader.Calendars, Redis: rdb,
			Throttler: &notify.Throttler{Redis: rdb, Config: func() directory.Throttle { return reloader.Directory().Throttling() }},
			Clock:     biz, Fallbacks: cfg.Fallbacks,
			Renderer: notify.Renderer{PublicURL: cfg.PublicURL}, Publisher: publisher, Schemas: schemas,
			BatchSize: cfg.ProcessorBatch, Log: log.With("component", "processor"),
		}
		g.Go(func() error { return p.Run(ctx) })
		g.Go(func() error { return p.RunSweeper(ctx, rdb, cfg.InstanceID, 5*time.Second) })
	}

	if cfg.Has(config.RoleDispatcher) {
		smtp, err := notify.NewSMTPChannel(cfg.SMTPAddr, cfg.SMTPFrom)
		if err != nil {
			return err
		}
		dlog := log.With("component", "dispatcher")
		// Breaker state is shared in Redis so the UI shows it whichever
		// replica serves the page.
		onChange := func(channel, state string) {
			_ = rdb.HSet(context.Background(), channelStateKey, channel,
				fmt.Sprintf("%s|%s|%d", state, cfg.InstanceID, time.Now().Unix())).Err()
		}
		const openFor = 15 * time.Second
		channels := map[string]notify.Channel{}
		for _, ch := range []notify.Channel{
			smtp,
			&notify.GatewayChannel{Kind: "sms", BaseURL: cfg.GatewayURL, Token: cfg.GatewayToken},
			&notify.GatewayChannel{Kind: "phone", BaseURL: cfg.GatewayURL, Token: cfg.GatewayToken},
		} {
			channels[ch.Name()] = notify.NewBreaking(ch, openFor, onChange, dlog)
			onChange(ch.Name(), "closed")
		}
		d := &notify.Dispatcher{
			Pool: pool, Channels: channels, Workers: cfg.DispatchWorkers, MaxAttempts: cfg.MaxAttempts,
			OpenFor: openFor, Log: dlog,
		}
		g.Go(func() error { return d.Run(ctx) })
	}

	if cfg.Has(config.RoleAPI) {
		hub := &api.Hub{Redis: rdb, Log: log.With("component", "live")}
		g.Go(func() error { return hub.Run(ctx) })
		srv := &api.Server{
			Store: store, Publisher: publisher, Engine: engine, RuleStore: ruleStore, Schemas: schemas,
			Directory: reloader.Directory, ConfigErrors: reloader.Errors, DefaultWindow: cfg.DedupWindow,
			Clock: biz, GatewayToken: cfg.GatewayToken, FX: reloader.FX, Correlation: reloader.Correlation,
			Calendars: reloader.Calendars, CheckRule: reloader.checkCalendars,
			ChannelStates: func(r *http.Request) map[string]string { return channelStates(r.Context(), rdb) },
			Notify:        func(ctx context.Context, kind string) error { return bus.NotifyConfig(ctx, rdb, kind) },
			ParseRaw: func(source string, raw []byte) *event.Envelope {
				if sources != nil {
					return ingest.NewEnvelope(sources.ParserFor(source), source, raw, nil)
				}
				return ingest.NewEnvelope(nil, source, raw, nil)
			},
			Hub: hub, UI: web.Dist(), Log: log.With("component", "api"),
			Info: map[string]any{"version": Version, "instance": cfg.InstanceID, "roles": cfg.RoleList(),
				"gatewayUrl": cfg.GatewayPublicURL, "mailUrl": "http://localhost:8025", "demoMode": cfg.DemoMode},
		}
		if sources != nil {
			srv.Sources = sources.Status
		}
		srv.Register(mux)
	}

	httpSrv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	g.Go(func() error {
		log.Info("http listening", "addr", cfg.HTTPAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	g.Go(func() error {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	})

	err = g.Wait()
	log.Info("stopped")
	return err
}

const channelStateKey = "kardan:channels:state"

// channelStates reports the worst breaker state per channel across
// dispatcher replicas (entries older than a minute are ignored).
func channelStates(ctx context.Context, rdb *redis.Client) map[string]string {
	raw, err := rdb.HGetAll(ctx, channelStateKey).Result()
	out := map[string]string{}
	if err != nil {
		return out
	}
	rank := map[string]int{"closed": 0, "half-open": 1, "open": 2}
	for ch, v := range raw {
		parts := strings.SplitN(v, "|", 3)
		if len(parts) != 3 {
			continue
		}
		if ts, err := strconv.ParseInt(parts[2], 10, 64); err == nil && parts[0] == "open" &&
			time.Since(time.Unix(ts, 0)) > 5*time.Minute {
			continue // stale: the replica that reported it is gone
		}
		if rank[parts[0]] >= rank[out[ch]] {
			out[ch] = parts[0]
		}
	}
	return out
}

func waitRedis(ctx context.Context, rdb *redis.Client, log *slog.Logger) error {
	delay := 250 * time.Millisecond
	for attempt := 1; ; attempt++ {
		err := rdb.Ping(ctx).Err()
		if err == nil {
			return nil
		}
		log.Warn("waiting for dependency", "dependency", "redis", "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			return fmt.Errorf("redis: %w", ctx.Err())
		case <-time.After(delay):
		}
		if delay < 5*time.Second {
			delay *= 2
		}
	}
}
