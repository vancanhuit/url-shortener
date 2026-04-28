// Package cli implements the cobra-based command tree for the url-shortener
// binary. The root command does not run a server itself -- subcommands like
// `run` and `migrate` (added in later phases) drive behaviour.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/vancanhuit/url-shortener/internal/buildinfo"
)

// Execute is the entry point used by main(). It builds the command tree and
// runs it, returning a process exit code.
func Execute() int {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		// cobra has already printed the error to stderr.
		_ = err
		return 1
	}
	return 0
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "url-shortener",
		Short:         "A small URL shortener service",
		Long:          "url-shortener is a tiny URL shortener written in Go.",
		Version:       buildinfo.Get().Version,
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	// Make `--version` print the same multi-line block as the `version`
	// subcommand for consistency.
	cmd.SetVersionTemplate(versionTemplate())

	cmd.AddCommand(
		newVersionCmd(),
		newConfigCmd(),
		newRunCmd(),
		newMigrateCmd(),
	)
	return cmd
}

// versionTemplate returns the template used for `url-shortener --version`.
func versionTemplate() string {
	info := buildinfo.Get()
	return fmt.Sprintf("url-shortener %s (commit %s, built %s)\n",
		info.Version, info.Commit, info.Date)
}
