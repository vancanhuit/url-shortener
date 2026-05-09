package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"github.com/vancanhuit/url-shortener/internal/config"
	"github.com/vancanhuit/url-shortener/internal/handlers"
)

// rateLimitExpiresIn is how long an idle per-IP token bucket survives
// in the in-memory store before it is evicted. Long enough to keep a
// returning legitimate user inside their established budget, short
// enough that a one-off scan from a transient IP doesn't pin memory.
const rateLimitExpiresIn = 3 * time.Minute

// buildCreateRateLimiter returns the middleware list to attach to
// `POST /api/v1/links`. When cfg.RateLimitRPS is 0 (the default) the
// returned slice is empty -- rate limiting is fully opt-in.
//
// The limiter uses Echo's bundled in-memory store keyed on the real
// client IP (Context.RealIP, which honors cfg.TrustedProxies). On a
// deny it returns a 429 with the project's standard JSON error
// envelope so SPA / API clients can branch on the stable code
// `rate_limited` rather than parsing prose. ID-extraction failures
// (which essentially can't happen with the IP extractor) fall through
// to a 500 with the same envelope.
//
// Multi-replica deployments share no state across processes here:
// the limiter is per-instance, so the effective ceiling is N * RPS
// behind a load balancer with weighted spread. That is intentional
// for a v1 limiter -- it costs nothing, can't go wrong under network
// partitions, and is plenty for the threat we care about (a single
// abuser hammering one node). Promote to Redis when N grows large
// enough that per-instance budgets stop being meaningful.
func buildCreateRateLimiter(cfg config.Config, logger *slog.Logger) []echo.MiddlewareFunc {
	if cfg.RateLimitRPS <= 0 {
		return nil
	}

	burst := cfg.RateLimitBurst
	if burst <= 0 {
		// Default burst = 2 * RPS so a legitimate client clicking
		// "create" a couple of times in a row stays inside the
		// bucket. Floor at 1 so a fractional RPS like 0.5 still
		// admits at least one request.
		burst = int(cfg.RateLimitRPS * 2)
		if burst < 1 {
			burst = 1
		}
	}

	store := middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
		Rate:      cfg.RateLimitRPS,
		Burst:     burst,
		ExpiresIn: rateLimitExpiresIn,
	})

	mw, err := middleware.RateLimiterConfig{
		Store: store,
		IdentifierExtractor: func(c *echo.Context) (string, error) {
			return c.RealIP(), nil
		},
		ErrorHandler: func(c *echo.Context, err error) error {
			logger.Warn("rate limiter: identifier extraction failed",
				"error", err, "remote", c.Request().RemoteAddr)
			return c.JSON(http.StatusInternalServerError,
				handlers.ErrorResponse{
					Error: "internal error",
					Code:  handlers.ErrCodeInternal,
				})
		},
		DenyHandler: func(c *echo.Context, identifier string, _ error) error {
			logger.Info("rate limit exceeded",
				"identifier", identifier,
				"path", c.Path(),
				"method", c.Request().Method,
			)
			return c.JSON(http.StatusTooManyRequests,
				handlers.ErrorResponse{
					Error: "rate limit exceeded",
					Code:  handlers.ErrCodeRateLimited,
				})
		},
	}.ToMiddleware()
	if err != nil {
		// ToMiddleware only errors when the config is incoherent
		// (e.g. nil Store), all of which are programming bugs in
		// this constructor. Fail fast at startup.
		panic("server: build rate limiter: " + err.Error())
	}

	logger.Info("rate limiter enabled",
		"endpoint", "POST /api/v1/links",
		"rps", cfg.RateLimitRPS,
		"burst", burst,
	)
	return []echo.MiddlewareFunc{mw}
}
