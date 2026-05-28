package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/vancanhuit/url-shortener/api"
	"github.com/vancanhuit/url-shortener/internal/config"
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
// real client IP (resolved via ipExtractor). Because the counter lives
// in Redis it is shared across all replicas, so the configured RPS
// budget is enforced globally -- not multiplied by the replica count.
//
// Fail-open: a Redis error is logged and the request is allowed through
// to avoid turning a cache outage into a service outage.
func buildCreateRateLimiter(
	cfg config.Config,
	rl rateLimiter,
	ipExtractor func(r *http.Request) string,
	logger *slog.Logger,
) []func(http.Handler) http.Handler {
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
	const window = time.Second

	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var ip string
			if ipExtractor != nil {
				ip = ipExtractor(r)
			} else {
				ip = r.RemoteAddr
			}
			key := rateLimitKeyPrefix + ip

			allowed, _, err := rl.RateLimit(r.Context(), key, burst, window)
			if err != nil {
				// Redis unavailable: fail open so a cache outage
				// never becomes a request outage.
				logger.Warn("rate limiter: backend error, allowing request",
					"error", err, "ip", ip)
				next.ServeHTTP(w, r)
				return
			}

			if !allowed {
				logger.Info("rate limit exceeded",
					"identifier", ip,
					"method", r.Method,
				)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(api.ErrorResponse{
					Error: "rate limit exceeded",
					Code:  api.ErrorResponseCodeRateLimited,
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	logger.Info("rate limiter enabled",
		"endpoint", "POST /api/v1/links",
		"backend", "redis",
		"rps", cfg.RateLimitRPS,
		"burst", burst,
	)
	return []func(http.Handler) http.Handler{mw}
}
