package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/vancanhuit/url-shortener/internal/buildinfo"
)

func newVersionCmd() *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "Print the build version, commit, and date",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "json",
				Usage: "emit machine-readable JSON",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			info := buildinfo.Get()
			if cmd.Bool("json") {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(info)
			}
			_, err := fmt.Fprintf(os.Stdout,
				"url-shortener %s\ncommit:  %s\nbuilt:   %s\n",
				info.Version, info.Commit, info.Date)
			return err
		},
	}
}
