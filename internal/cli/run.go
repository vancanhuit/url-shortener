package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/urfave/cli/v3"

	"github.com/vancanhuit/url-shortener/internal/cache"
	"github.com/vancanhuit/url-shortener/internal/config"
	"github.com/vancanhuit/url-shortener/internal/migrate"
	"github.com/vancanhuit/url-shortener/internal/server"
	"github.com/vancanhuit/url-shortener/internal/shortener"
	"github.com/vancanhuit/url-shortener/internal/store"
)

var (
	migrateUpFn       = migrate.Up
	migrateVersionsFn = migrate.Versions
)

func ensureSchemaCurrent(ctx context.Context, cfg config.Config) error {
	if cfg.AutoMigrate {
		return migrateUpFn(ctx, cfg.DatabaseURL)
	}

	current, latest, err := migrateVersionsFn(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	if current < latest {
		return fmt.Errorf(
			"database schema is behind embedded migrations (current=%d latest=%d); run `url-shortener migrate up` or set URL_SHORTENER_AUTO_MIGRATE=true",
			current,
			latest,
		)
	}
	return nil
}

func newRunCmd() *cli.Command {
	return &cli.Command{
		Name:  "run",
		Usage: "Run the HTTP server",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			logger, err := newLogger(cfg, os.Stderr)
			if err != nil {
				return err
			}
			logger.Info("starting url-shortener", "env", cfg.Env, "addr", cfg.Addr)

			// Postgres is a required runtime dependency (enforced by
			// config.Validate); DatabaseURL is guaranteed non-empty here.
			if cfg.AutoMigrate {
				logger.Info("auto_migrate=true; applying migrations before serving")
			}
			if err := ensureSchemaCurrent(ctx, cfg); err != nil {
				return err
			}
			st, err := store.NewWithPool(ctx, cfg.DatabaseURL, store.PoolConfig{
				MaxConns:          cfg.DBMaxConns,
				MinConns:          cfg.DBMinConns,
				MaxConnLifetime:   cfg.DBMaxConnLifetime,
				MaxConnIdleTime:   cfg.DBMaxConnIdleTime,
				HealthCheckPeriod: cfg.DBHealthCheckPeriod,
			})
			if err != nil {
				return err
			}
			defer st.Close()

			// Redis is a required dependency (enforced by config.Validate),
			// so RedisURL is guaranteed to be non-empty here.
			cc, err := cache.New(ctx, cfg.RedisURL)
			if err != nil {
				return err
			}
			defer func() { _ = cc.Close() }()

			// Cancel the run context on SIGINT/SIGTERM so the server can
			// shut down gracefully.
			ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			gen, err := shortener.NewGenerator(cfg.CodeLength)
			if err != nil {
				return err
			}

			srv := server.New(cfg, logger, server.Deps{ //nolint:contextcheck // slogRequestLogger uses per-request context internally, not the server lifetime ctx
				Store:     st,
				Cache:     cc,
				Generator: gen,
			})
			return srv.Run(ctx)
		},
	}
}
