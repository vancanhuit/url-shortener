// Package cli implements the urfave/cli-based command tree for the url-shortener
// binary. The root command does not run a server itself -- subcommands like
// `run` and `migrate` drive behavior.
package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/vancanhuit/url-shortener/internal/buildinfo"
)

// Execute is the entry point used by main(). It builds the command tree and
// runs it, returning a process exit code.
func Execute() int {
	app := newApp()
	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func newApp() *cli.Command {
	info := buildinfo.Get()
	return &cli.Command{
		Name:    "url-shortener",
		Usage:   "A small URL shortener service",
		Version: fmt.Sprintf("%s (commit %s, built %s)", info.Version, info.Commit, info.Date),
		Commands: []*cli.Command{
			newVersionCmd(),
			newConfigCmd(),
			newRunCmd(),
			newMigrateCmd(),
			newCleanupCmd(),
			newHealthcheckCmd(),
		},
	}
}
