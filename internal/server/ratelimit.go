package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/vancanhuit/url-shortener/internal/config"
	"github.com/vancanhuit/url-shortener/internal/handlers"
)

// rateLimiter is the shared-state backend for the per-IP rate limiter.
// The production implementation is *cache.Client; tests inject a fake.
// Callers should allow the request when err is non-nil (fail-open).
type rateLimiter interface {
	RateLimit(ctx context.Context, key string, limit int, window time.Duration) (bool, int, error)
}

// rateLimitKeyPrefix namespaces the per-IP keys so they never collide
// with the redirect-lookup cache entries written by the handlers layer.
const rateLimitKeyPrefix = "ratelimit:create:"

// buildCreateRateLimiter returns the middleware list to attach to
// `POST /api/v1/links`. When cfg.RateLimitRPS is 0 (the default) the
// returned slice is empty -- rate limiting is fully opt-in.
//
// The limiter uses a Redis fixed-window counter (via rl) keyed on the
// real client IP (Context.RealIP, which honors cfg.TrustedProxies).
// Because the counter lives in Redis it is shared across all replicas,
// so the configured RPS budget is enforced globally -- not multiplied
// by the replica count. On a deny it returns 429 with the project's
// standard JSON error envelope.
//
// Fail-open: a Redis error is logged and the request is allowed through
// to avoid turning a cache outage into a service outage.
//
// Fixed-window trade-off: at a window boundary a client can observe up
// to 2 × burst requests. Acceptable for abuse prevention; switch to a
// sliding-window (sorted-set) implementation if strict per-second
// budgets are required.
func buildCreateRateLimiter(cfg config.Config, rl rateLimiter, logger *slog.Logger) []echo.MiddlewareFunc {
	if cfg.RateLimitRPS <= 0 {
		return nil
	}

	burst := cfg.RateLimitBurst
	if burst <= 0 {
		// Default burst = 2 × RPS so a legitimate client clicking
		// "create" a couple of times in a row stays inside the
		// budget. Floor at 1 so a fractional RPS like 0.5 still
		// admits at least one request.
		burst = max(int(cfg.RateLimitRPS*2), 1)
	}

	// Fixed window of 1 second: `burst` requests per second per IP.
	// Using 1 s keeps the key TTL short and keeps the math simple;
	// the RPS value determines the steady-state budget when burst is
	// left at its default (2 × RPS) and the operator tunes both.
	const window = time.Second

	mw := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			ip := c.RealIP()
			key := rateLimitKeyPrefix + ip

			allowed, _, err := rl.RateLimit(c.Request().Context(), key, burst, window)
			if err != nil {
				// Redis unavailable: fail open so a cache outage
				// never becomes a request outage.
				logger.Warn("rate limiter: backend error, allowing request",
					"error", err, "ip", ip)
				return next(c)
			}

			if !allowed {
				logger.Info("rate limit exceeded",
					"identifier", ip,
					"path", c.Path(),
					"method", c.Request().Method,
				)
				return c.JSON(http.StatusTooManyRequests,
					handlers.ErrorResponse{
						Error: "rate limit exceeded",
						Code:  handlers.ErrCodeRateLimited,
					})
			}
			return next(c)
		}
	}

	logger.Info("rate limiter enabled",
		"endpoint", "POST /api/v1/links",
		"backend", "redis",
		"rps", cfg.RateLimitRPS,
		"burst", burst,
	)
	return []echo.MiddlewareFunc{mw}
}
