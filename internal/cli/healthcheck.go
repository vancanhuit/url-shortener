package cli

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

// newHealthcheckCmd is the binary's own probe -- handy as a Docker
// HEALTHCHECK in distroless images that ship neither curl nor wget.
//
// Usage in compose.yaml:
//
//	healthcheck:
//	  test: ["CMD", "/usr/local/bin/url-shortener", "healthcheck"]
func newHealthcheckCmd() *cobra.Command {
	var (
		url     string
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Probe the local /healthz endpoint and exit 0 when it returns 200",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("healthcheck: status %d", resp.StatusCode)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "http://127.0.0.1:8080/healthz", "URL to probe")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Second, "request timeout")
	return cmd
}
