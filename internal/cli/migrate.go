package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/vancanhuit/url-shortener/internal/config"
	"github.com/vancanhuit/url-shortener/internal/migrate"
)

// resolveDBURL picks the explicit --database-url flag when given, otherwise
// falls back to the value loaded from the environment.
func resolveDBURL(cmd *cli.Command) (string, error) {
	if url := cmd.String("database-url"); url != "" {
		return url, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	return cfg.DatabaseURL, nil
}

func dbURLFlag() *cli.StringFlag {
	return &cli.StringFlag{
		Name:  "database-url",
		Usage: "Postgres connection string (overrides URL_SHORTENER_DATABASE_URL)",
	}
}

func newMigrateCmd() *cli.Command {
	return &cli.Command{
		Name:  "migrate",
		Usage: "Apply, roll back, or inspect database migrations using the embedded SQL files.",
		Commands: []*cli.Command{
			newMigrateUpCmd(),
			newMigrateDownCmd(),
			newMigrateStatusCmd(),
			newMigrateRedoCmd(),
			newMigrateCreateCmd(),
		},
	}
}

func newMigrateUpCmd() *cli.Command {
	return &cli.Command{
		Name:  "up",
		Usage: "Apply all pending migrations",
		Flags: []cli.Flag{dbURLFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			url, err := resolveDBURL(cmd)
			if err != nil {
				return err
			}
			return migrate.Up(ctx, url)
		},
	}
}

func newMigrateDownCmd() *cli.Command {
	return &cli.Command{
		Name:  "down",
		Usage: "Roll back the most recent migration",
		Flags: []cli.Flag{dbURLFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			url, err := resolveDBURL(cmd)
			if err != nil {
				return err
			}
			return migrate.Down(ctx, url)
		},
	}
}

func newMigrateStatusCmd() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "Print the migration status",
		Flags: []cli.Flag{dbURLFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			url, err := resolveDBURL(cmd)
			if err != nil {
				return err
			}
			return migrate.Status(ctx, url)
		},
	}
}

func newMigrateRedoCmd() *cli.Command {
	return &cli.Command{
		Name:  "redo",
		Usage: "Roll back the most recent migration and re-apply it",
		Flags: []cli.Flag{dbURLFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			url, err := resolveDBURL(cmd)
			if err != nil {
				return err
			}
			return migrate.Redo(ctx, url)
		},
	}
}

func newMigrateCreateCmd() *cli.Command {
	return &cli.Command{
		Name:      "create",
		Usage:     "Create a new sequential SQL migration file",
		ArgsUsage: "<name>",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "dir", Value: "migrations", Usage: "Directory to write the new migration file into"},
			&cli.StringFlag{Name: "type", Value: "sql", Usage: "Migration type: sql or go"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 {
				return fmt.Errorf("migrate create: expected exactly one argument <name>, got %d", cmd.NArg())
			}
			name := cmd.Args().Get(0)
			dir := cmd.String("dir")
			if err := migrate.Create(dir, name, cmd.String("type")); err != nil {
				return err
			}
			_, err := fmt.Fprintf(os.Stdout, "Created migration: %s/%s\n", dir, name)
			return err
		},
	}
}
