package cli

import (
	"context"
	"encoding/json"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/vancanhuit/url-shortener/internal/config"
)

func newConfigCmd() *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "Print the resolved configuration (with secrets redacted)",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(cfg.Redacted())
		},
	}
}
