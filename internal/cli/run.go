package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/vancanhuit/url-shortener/internal/config"
)

// errRunNotImplemented is returned by `run` until the HTTP server is wired up
// in a later phase.
var errRunNotImplemented = errors.New("`run` is not implemented yet (added in a later phase)")

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the HTTP server",
		Long:  "Run the HTTP server. Currently a stub; the server is wired up in a later phase.",
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
			return errRunNotImplemented
		},
	}
	return cmd
}
