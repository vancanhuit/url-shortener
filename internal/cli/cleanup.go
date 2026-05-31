package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/vancanhuit/url-shortener/internal/store"
)

// newCleanupCmd hard-deletes link rows that have been retired --
// soft-deleted, or past their expires_at -- for longer than --grace.
//
// Intended to be run as a periodic batch job (cron, k8s CronJob,
// systemd timer) rather than in-process: keeping it out of the long-
// running server process avoids one more goroutine to reason about
// during shutdown, and lets operators tune cadence + retention
// independently of the request-serving deployment.
func newCleanupCmd() *cli.Command {
	return &cli.Command{
		Name:  "cleanup",
		Usage: "Hard-delete link rows that have been soft-deleted or expired for longer than --grace.",
		Flags: []cli.Flag{
			dbURLFlag(),
			&cli.DurationFlag{
				Name:  "grace",
				Value: 30 * 24 * time.Hour,
				Usage: "minimum age (deleted_at or expires_at older than now()-grace) before a row is purged",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			url, err := resolveDBURL(cmd)
			if err != nil {
				return err
			}
			grace := cmd.Duration("grace")
			if grace <= 0 {
				return fmt.Errorf("cleanup: --grace must be > 0, got %s", grace)
			}

			s, err := store.New(ctx, url)
			if err != nil {
				return fmt.Errorf("cleanup: open store: %w", err)
			}
			defer s.Close()

			start := time.Now()
			n, err := s.PurgeExpiredAndDeleted(ctx, s.Pool(), grace)
			if err != nil {
				return err
			}
			slog.New(slog.NewTextHandler(os.Stdout, nil)).Info("cleanup completed",
				"rows_deleted", n,
				"grace", grace.String(),
				"duration", time.Since(start).String(),
			)
			return nil
		},
	}
}
