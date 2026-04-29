package cli

import (
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/vancanhuit/url-shortener/internal/cache"
	"github.com/vancanhuit/url-shortener/internal/config"
	"github.com/vancanhuit/url-shortener/internal/migrate"
	"github.com/vancanhuit/url-shortener/internal/server"
	"github.com/vancanhuit/url-shortener/internal/store"
)

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run the HTTP server",
		Long: "Run the HTTP server. Loads config from environment, opens the " +
			"Postgres pool, mounts routes, and serves until SIGINT/SIGTERM " +
			"triggers a graceful shutdown.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			logger, err := newLogger(cfg, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			logger.Info("starting url-shortener", "env", cfg.Env, "addr", cfg.Addr)

			// Database and cache are both optional: /healthz answers without
			// either, and /readyz simply omits checks for missing deps. This
			// keeps first-boot smoke tests friendly.
			var st *store.Store
			if cfg.DatabaseURL != "" {
				if cfg.AutoMigrate {
					logger.Info("auto_migrate=true; applying migrations before serving")
					if err := migrate.Up(cmd.Context(), cfg.DatabaseURL); err != nil {
						return err
					}
				}
				st, err = store.New(cmd.Context(), cfg.DatabaseURL)
				if err != nil {
					return err
				}
				defer st.Close()
			} else {
				logger.Warn("URL_SHORTENER_DATABASE_URL is empty; running without a database")
			}

			var cc *cache.Client
			if cfg.RedisURL != "" {
				cc, err = cache.New(cmd.Context(), cfg.RedisURL)
				if err != nil {
					return err
				}
				defer func() { _ = cc.Close() }()
			} else {
				logger.Warn("URL_SHORTENER_REDIS_URL is empty; running without a cache")
			}

			// Cancel the run context on SIGINT/SIGTERM so the server can
			// shut down gracefully.
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			srv := server.New(cfg, logger, server.Deps{Store: st, Cache: cc})
			return srv.Run(ctx)
		},
	}
}
