// Package app wires components together according to the replica's roles.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"
	"golang.org/x/sync/errgroup"

	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/api"
	"github.com/undeluro/kardan-alerts/internal/directory"
	"github.com/undeluro/kardan-alerts/internal/ingest"
	"github.com/undeluro/kardan-alerts/internal/notify"
	"github.com/undeluro/kardan-alerts/internal/platform/config"
	"github.com/undeluro/kardan-alerts/internal/platform/kafka"
	"github.com/undeluro/kardan-alerts/internal/platform/pg"
	"github.com/undeluro/kardan-alerts/internal/processor"
	"github.com/undeluro/kardan-alerts/internal/rules"
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

	dir, err := directory.Load(filepath.Join(cfg.ConfigDir, "teams.yaml"))
	if err != nil {
		return fmt.Errorf("load teams: %w", err)
	}
	getDir := func() *directory.Directory { return dir }

	engine := rules.NewEngine()
	list, loadErrs := rules.LoadDir(filepath.Join(cfg.ConfigDir, "rules"))
	rs := rules.Build(list, cfg.DedupWindow)
	rs.Errors = append(loadErrs, rs.Errors...)
	engine.Swap(rs)
	for _, e := range rs.Errors {
		log.Warn("rule skipped", "err", e)
	}
	log.Info("rules loaded", "count", len(rs.Rules), "skipped", len(rs.Errors))

	store := &alerting.Store{Pool: pool}
	publisher := &alerting.Publisher{Redis: rdb}
	g, ctx := errgroup.WithContext(ctx)
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

	if cfg.Has(config.RoleIngest) {
		prod, err := kafka.NewClient(ctx, cfg.KafkaBrokers, log,
			kgo.ProducerLinger(0),
			kgo.ProducerBatchCompression(kgo.Lz4Compression(), kgo.NoCompression()),
		)
		if err != nil {
			return err
		}
		defer prod.Close()
		(&ingest.HTTPHandler{Producer: &ingest.Producer{Kafka: prod, Topic: cfg.EventsTopic}, Log: log}).Register(mux)
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
			Kafka: cons, Pool: pool, Engine: engine, Directory: getDir,
			Renderer: notify.Renderer{PublicURL: cfg.PublicURL}, Publisher: publisher,
			BatchSize: cfg.ProcessorBatch, Log: log.With("component", "processor"),
		}
		g.Go(func() error { return p.Run(ctx) })
	}

	if cfg.Has(config.RoleDispatcher) {
		smtp, err := notify.NewSMTPChannel(cfg.SMTPAddr, cfg.SMTPFrom)
		if err != nil {
			return err
		}
		dlog := log.With("component", "dispatcher")
		d := &notify.Dispatcher{
			Pool: pool,
			Channels: map[string]notify.Channel{
				"email": smtp,
				"sms":   &notify.LogChannel{Channel: "sms", Log: dlog},
				"phone": &notify.LogChannel{Channel: "phone", Log: dlog},
			},
			Workers: cfg.DispatchWorkers, MaxAttempts: cfg.MaxAttempts, Log: dlog,
		}
		g.Go(func() error { return d.Run(ctx) })
	}

	if cfg.Has(config.RoleAPI) {
		hub := &api.Hub{Redis: rdb, Log: log.With("component", "live")}
		g.Go(func() error { return hub.Run(ctx) })
		srv := &api.Server{
			Store: store, Publisher: publisher, Engine: engine, Directory: getDir, Hub: hub,
			UI: web.Dist(), Log: log.With("component", "api"),
			Info: map[string]any{"version": Version, "instance": cfg.InstanceID, "roles": cfg.RoleList()},
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
