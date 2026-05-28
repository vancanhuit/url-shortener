package cli

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/urfave/cli/v3"
)

// newHealthcheckCmd is the binary's own probe -- handy as a Docker
// HEALTHCHECK in distroless images that ship neither curl nor wget.
//
// Usage in compose.yaml:
//
//	healthcheck:
//	  test: ["CMD", "/usr/local/bin/url-shortener", "healthcheck"]
//
// For TLS-fronted services (the `tls` compose profile), pass
// `--url=https://127.0.0.1:8443/healthz --insecure` so the probe
// skips cert verification -- the certificate is intended for the
// outside world, not the in-container loopback hop.
func newHealthcheckCmd() *cli.Command {
	return &cli.Command{
		Name:  "healthcheck",
		Usage: "Probe the local /healthz endpoint and exit 0 when it returns 200",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "url",
				Value: "http://127.0.0.1:8080/healthz",
				Usage: "URL to probe",
			},
			&cli.DurationFlag{
				Name:  "timeout",
				Value: 2 * time.Second,
				Usage: "request timeout",
			},
			&cli.BoolFlag{
				Name:  "insecure",
				Usage: "skip TLS certificate verification (use with https:// URLs in compose healthchecks)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			url := cmd.String("url")
			timeout := cmd.Duration("timeout")
			insecure := cmd.Bool("insecure")

			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			client := http.DefaultClient
			if insecure {
				// New client per call so we don't mutate
				// http.DefaultTransport. The probe is short-lived
				// and runs once per healthcheck invocation, so the
				// allocation cost is irrelevant.
				client = &http.Client{
					Transport: &http.Transport{
						TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // intentional for in-container loopback probe
					},
					Timeout: timeout,
				}
			}
			resp, err := client.Do(req)
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
}
