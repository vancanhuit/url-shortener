package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

// errMigrateNotImplemented is returned by `migrate *` until the migration
// runner is wired up in a later phase.
var errMigrateNotImplemented = errors.New("`migrate` is not implemented yet (added in a later phase)")

func newMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run database migrations",
		Long:  "Apply, roll back, or inspect database migrations. Currently a stub.",
	}
	for _, sub := range []*cobra.Command{
		{Use: "up", Short: "Apply all pending migrations", RunE: notImplemented},
		{Use: "down", Short: "Roll back the most recent migration", RunE: notImplemented},
		{Use: "status", Short: "Print migration status", RunE: notImplemented},
	} {
		cmd.AddCommand(sub)
	}
	return cmd
}

func notImplemented(*cobra.Command, []string) error {
	return errMigrateNotImplemented
}
