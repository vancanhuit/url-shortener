package cli

import (
	"github.com/spf13/cobra"

	"github.com/vancanhuit/url-shortener/internal/config"
	"github.com/vancanhuit/url-shortener/internal/migrate"
)

// migrateFlags is shared by every `migrate` subcommand. The --database-url
// flag overrides URL_SHORTENER_DATABASE_URL, so callers (e.g. CI) can apply
// migrations against a separately-configured database (e.g. one named via
// URL_SHORTENER_TEST_DATABASE_URL) without polluting the app's runtime env.
type migrateFlags struct {
	databaseURL string
}

func (f *migrateFlags) bind(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&f.databaseURL, "database-url", "",
		"Postgres connection string (overrides URL_SHORTENER_DATABASE_URL)")
}

// resolve picks the explicit --database-url flag when given, otherwise
// falls back to the value loaded from the environment.
func (f *migrateFlags) resolve() (string, error) {
	if f.databaseURL != "" {
		return f.databaseURL, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	return cfg.DatabaseURL, nil
}

func newMigrateCmd() *cobra.Command {
	flags := &migrateFlags{}
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run database migrations",
		Long:  "Apply, roll back, or inspect database migrations using the embedded SQL files.",
	}
	flags.bind(cmd)
	cmd.AddCommand(
		newMigrateUpCmd(flags),
		newMigrateDownCmd(flags),
		newMigrateStatusCmd(flags),
	)
	return cmd
}

func newMigrateUpCmd(flags *migrateFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Apply all pending migrations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			url, err := flags.resolve()
			if err != nil {
				return err
			}
			return migrate.Up(cmd.Context(), url)
		},
	}
}

func newMigrateDownCmd(flags *migrateFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Roll back the most recent migration",
		RunE: func(cmd *cobra.Command, _ []string) error {
			url, err := flags.resolve()
			if err != nil {
				return err
			}
			return migrate.Down(cmd.Context(), url)
		},
	}
}

func newMigrateStatusCmd(flags *migrateFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print the migration status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			url, err := flags.resolve()
			if err != nil {
				return err
			}
			return migrate.Status(cmd.Context(), url)
		},
	}
}
