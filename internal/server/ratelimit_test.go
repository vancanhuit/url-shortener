package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/vancanhuit/url-shortener/internal/config"
	"github.com/vancanhuit/url-shortener/internal/handlers"
)

// TestBuildCreateRateLimiter_DisabledByDefault: with RateLimitRPS=0
// the constructor returns no middleware -- the existing
// `links.Mount(e)` call sites stay completely unaffected when the
// operator hasn't opted in.
func TestBuildCreateRateLimiter_DisabledByDefault(t *testing.T) {
	t.Parallel()
	cfg := config.Config{} // zero value, RateLimitRPS=0
	if got := buildCreateRateLimiter(cfg, slog.New(slog.DiscardHandler)); got != nil {
		t.Errorf("buildCreateRateLimiter(rps=0) = %d middleware, want nil", len(got))
	}
}

// TestBuildCreateRateLimiter_DeniesAfterBurst: with a tiny burst
// budget, the (burst+1)-th request from the same IP returns 429 with
// the standard JSON envelope and `code: rate_limited`. Earlier
// requests within the burst pass through to the wrapped handler.
func TestBuildCreateRateLimiter_DeniesAfterBurst(t *testing.T) {
	t.Parallel()
	cfg := config.Config{RateLimitRPS: 1, RateLimitBurst: 2}
	mws := buildCreateRateLimiter(cfg, slog.New(slog.DiscardHandler))
	if len(mws) != 1 {
		t.Fatalf("buildCreateRateLimiter mws = %d, want 1", len(mws))
	}

	e := echo.New()
	hits := 0
	e.POST("/x", func(c *echo.Context) error {
		hits++
		return c.NoContent(http.StatusCreated)
	}, mws...)

	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(""))
		req.RemoteAddr = "203.0.113.7:1234"
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	// Burst of 2: both should pass (201).
	if rec := do(); rec.Code != http.StatusCreated {
		t.Fatalf("first call status = %d, want 201", rec.Code)
	}
	if rec := do(); rec.Code != http.StatusCreated {
		t.Fatalf("second call status = %d, want 201", rec.Code)
	}

	// Third call within the same instant blows the bucket -> 429.
	rec := do()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third call status = %d, want 429", rec.Code)
	}
	if hits != 2 {
		t.Errorf("wrapped handler invocations = %d, want 2 (limiter must short-circuit)", hits)
	}

	body, _ := io.ReadAll(rec.Body)
	var resp handlers.ErrorResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v (body=%q)", err, string(body))
	}
	if resp.Code != handlers.ErrCodeRateLimited {
		t.Errorf("error code = %q, want %q", resp.Code, handlers.ErrCodeRateLimited)
	}
	if !strings.Contains(strings.ToLower(resp.Error), "rate limit") {
		t.Errorf("error message = %q, want substring 'rate limit'", resp.Error)
	}
}

// TestBuildCreateRateLimiter_PerIPIsolation: distinct client IPs each
// get their own bucket, so one abuser cannot starve a different
// client. Two requests from IP A both pass even after IP B has been
// throttled.
func TestBuildCreateRateLimiter_PerIPIsolation(t *testing.T) {
	t.Parallel()
	cfg := config.Config{RateLimitRPS: 1, RateLimitBurst: 1}
	mws := buildCreateRateLimiter(cfg, slog.New(slog.DiscardHandler))

	e := echo.New()
	e.POST("/x", func(c *echo.Context) error {
		return c.NoContent(http.StatusCreated)
	}, mws...)

	do := func(ip string) int {
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(""))
		req.RemoteAddr = ip + ":1234"
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec.Code
	}

	// Burn IP B's bucket.
	if got := do("198.51.100.7"); got != http.StatusCreated {
		t.Fatalf("B first = %d, want 201", got)
	}
	if got := do("198.51.100.7"); got != http.StatusTooManyRequests {
		t.Fatalf("B second = %d, want 429", got)
	}

	// IP A is untouched.
	if got := do("203.0.113.9"); got != http.StatusCreated {
		t.Errorf("A first = %d, want 201 (per-IP isolation broken)", got)
	}
}

// TestBuildCreateRateLimiter_BurstDerivedFromRPS: a 0 burst with a
// non-zero RPS must be filled in as max(1, 2*RPS) so a fractional
// RPS like 0.5 still admits at least one request before throttling.
func TestBuildCreateRateLimiter_BurstDerivedFromRPS(t *testing.T) {
	t.Parallel()
	cfg := config.Config{RateLimitRPS: 0.25, RateLimitBurst: 0}
	mws := buildCreateRateLimiter(cfg, slog.New(slog.DiscardHandler))

	e := echo.New()
	e.POST("/x", func(c *echo.Context) error { return c.NoContent(http.StatusCreated) }, mws...)

	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(""))
	req.RemoteAddr = "192.0.2.1:1234"
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Errorf("first call status = %d, want 201 (burst floor of 1 must apply)", rec.Code)
	}
}
