// Package pg connects to Postgres and applies embedded migrations.
package pg

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/undeluro/kardan-alerts/internal/store/migrations"
)

// Connect opens a pool, retrying until Postgres accepts connections or ctx ends.
func Connect(ctx context.Context, url string, log *slog.Logger) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse POSTGRES_URL: %w", err)
	}
	cfg.MaxConns = 32
	var pool *pgxpool.Pool
	err = retry(ctx, log, "postgres", func() error {
		p, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			return err
		}
		if err := p.Ping(ctx); err != nil {
			p.Close()
			return err
		}
		pool = p
		return nil
	})
	return pool, err
}

// Migrate applies pending migrations. A Postgres advisory lock serialises
// concurrent replicas starting at the same time.
func Migrate(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	return migrate(ctx, db, log)
}

func migrate(ctx context.Context, db *sql.DB, log *slog.Logger) error {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS,
		goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	for _, r := range results {
		log.Info("migration applied", "version", r.Source.Version, "file", r.Source.Path, "took", r.Duration)
	}
	return nil
}

// retry calls fn with capped exponential backoff until it succeeds or ctx ends.
func retry(ctx context.Context, log *slog.Logger, what string, fn func() error) error {
	delay := 250 * time.Millisecond
	for attempt := 1; ; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		log.Warn("waiting for dependency", "dependency", what, "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %w (last error: %v)", what, ctx.Err(), err)
		case <-time.After(delay):
		}
		if delay < 5*time.Second {
			delay *= 2
		}
	}
}
