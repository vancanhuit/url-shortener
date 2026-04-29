package cli

import (
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/vancanhuit/url-shortener/internal/config"
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

			// Database is optional at this point: we still want /healthz to
			// answer if Postgres is unconfigured (handy for first-boot smoke
			// tests). When configured, /readyz pings it.
			var st *store.Store
			if cfg.DatabaseURL != "" {
				st, err = store.New(cmd.Context(), cfg.DatabaseURL)
				if err != nil {
					return err
				}
				defer st.Close()
			} else {
				logger.Warn("URL_SHORTENER_DATABASE_URL is empty; running without a database")
			}

			// Cancel the run context on SIGINT/SIGTERM so the server can
			// shut down gracefully.
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			srv := server.New(cfg, logger, st)
			return srv.Run(ctx)
		},
	}
}
