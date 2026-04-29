// Package handlers contains the HTTP handlers for the url-shortener API.
//
// This file implements the operational endpoints (`/healthz`, `/readyz`,
// `/version`) used by orchestrators and humans to verify the running binary.
package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/vancanhuit/url-shortener/internal/buildinfo"
)

// readinessChecker reports whether a single dependency is healthy. The
// returned error (if any) is recorded in the readyz response.
type readinessChecker interface {
	CheckReady(ctx context.Context) error
}

// pingFunc is a tiny adapter so we can register checks without defining a
// type; e.g. h.AddReadinessCheck("db", store.Ping).
type pingFunc func(ctx context.Context) error

func (f pingFunc) CheckReady(ctx context.Context) error { return f(ctx) }

// Operational bundles the operational handlers behind a small struct so we
// can register multiple readiness checks at wiring time.
type Operational struct {
	checks map[string]readinessChecker

	// readyTimeout caps each individual readiness check so a slow dep
	// can't starve the response. 2s is plenty for a TCP ping.
	readyTimeout time.Duration
}

// NewOperational returns a handler bundle with no readiness checks.
// Add them via AddReadinessCheck before mounting.
func NewOperational() *Operational {
	return &Operational{
		checks:       map[string]readinessChecker{},
		readyTimeout: 2 * time.Second,
	}
}

// AddReadinessCheck registers a named dependency check used by /readyz.
func (h *Operational) AddReadinessCheck(name string, check func(ctx context.Context) error) {
	h.checks[name] = pingFunc(check)
}

// Mount registers /healthz, /readyz, /version on e.
func (h *Operational) Mount(e *echo.Echo) {
	e.GET("/healthz", h.Healthz)
	e.GET("/readyz", h.Readyz)
	e.GET("/version", h.Version)
}

// Healthz is the liveness probe: it returns 200 as long as the process is
// running and the HTTP stack is responsive. It deliberately has no
// dependencies so a flapping database does not cause restarts.
func (h *Operational) Healthz(c *echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// Readyz runs every registered readiness check and returns 200 only when all
// pass. The response body lists per-check results so operators can see which
// dependency is unhappy.
func (h *Operational) Readyz(c *echo.Context) error {
	results := make(map[string]string, len(h.checks)+1)
	allOK := true

	for name, ck := range h.checks {
		ctx, cancel := context.WithTimeout(c.Request().Context(), h.readyTimeout)
		err := ck.CheckReady(ctx)
		cancel()
		if err != nil {
			results[name] = "error: " + err.Error()
			allOK = false
		} else {
			results[name] = "ok"
		}
	}

	if allOK {
		results["status"] = "ok"
		return c.JSON(http.StatusOK, results)
	}
	results["status"] = "unready"
	return c.JSON(http.StatusServiceUnavailable, results)
}

// Version returns the build metadata baked into the binary.
func (h *Operational) Version(c *echo.Context) error {
	return c.JSON(http.StatusOK, buildinfo.Get())
}
