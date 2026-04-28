package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/vancanhuit/url-shortener/internal/config"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Print the resolved configuration (with secrets redacted)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(cfg.Redacted())
		},
	}
	return cmd
}
