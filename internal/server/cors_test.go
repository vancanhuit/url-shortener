package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/vancanhuit/url-shortener/internal/config"
)

// TestBuildCORS_DisabledByDefault: the empty allow-list returns nil,
// so the middleware chain stays free of CORS overhead for the typical
// same-origin SPA + API deployment.
func TestBuildCORS_DisabledByDefault(t *testing.T) {
	t.Parallel()
	if got := buildCORS(config.Config{}, slog.New(slog.DiscardHandler)); got != nil {
		t.Errorf("buildCORS(empty) = non-nil, want nil")
	}
}

// TestBuildCORS_AllEmptyEntriesIsTreatedAsOff: viper splits a stray
// `,,` env value into empty entries -- they should be treated as
// "off", not as "an explicit empty allow-list" (which CORSConfig
// would reject as invalid at construction time).
func TestBuildCORS_AllEmptyEntriesIsTreatedAsOff(t *testing.T) {
	t.Parallel()
	cfg := config.Config{CORSAllowedOrigins: []string{"", ""}}
	if got := buildCORS(cfg, slog.New(slog.DiscardHandler)); got != nil {
		t.Errorf("buildCORS(only-empties) = non-nil, want nil")
	}
}

// newCORSEchoForTest wires the CORS middleware onto a stub handler
// returning 200, so the headers Echo's CORS layer adds are observable
// in the recorder.
func newCORSEchoForTest(t *testing.T, origins ...string) *echo.Echo {
	t.Helper()
	cfg := config.Config{CORSAllowedOrigins: origins}
	mw := buildCORS(cfg, slog.New(slog.DiscardHandler))
	if mw == nil {
		t.Fatalf("buildCORS returned nil for non-empty origins %v", origins)
	}
	e := echo.New()
	e.Use(mw)
	e.POST("/api/v1/links", func(c *echo.Context) error {
		return c.NoContent(http.StatusOK)
	})
	return e
}

// TestBuildCORS_AllowedOriginGetsACAOHeader: a simple POST from an
// allow-listed origin must have its origin echoed in the
// Access-Control-Allow-Origin response header so the browser exposes
// the response body to the page.
func TestBuildCORS_AllowedOriginGetsACAOHeader(t *testing.T) {
	t.Parallel()
	e := newCORSEchoForTest(t, "https://allowed.example")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/links", nil)
	req.Header.Set(echo.HeaderOrigin, "https://allowed.example")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get(echo.HeaderAccessControlAllowOrigin); got != "https://allowed.example" {
		t.Errorf("ACAO = %q, want allow-list echo", got)
	}
}

// TestBuildCORS_DisallowedOriginGetsNoACAOHeader: a POST from an
// origin that isn't on the allow-list must not pick up an ACAO header
// (the browser will then refuse to expose the response body), but
// the request itself still reaches the handler -- CORS is enforced
// in the browser, not on the server.
func TestBuildCORS_DisallowedOriginGetsNoACAOHeader(t *testing.T) {
	t.Parallel()
	e := newCORSEchoForTest(t, "https://allowed.example")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/links", nil)
	req.Header.Set(echo.HeaderOrigin, "https://attacker.example")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if got := rec.Header().Get(echo.HeaderAccessControlAllowOrigin); got != "" {
		t.Errorf("ACAO = %q, want empty for disallowed origin", got)
	}
}

// TestBuildCORS_PreflightAdvertisesMethodsHeadersAndMaxAge: a
// preflight OPTIONS request from an allow-listed origin must surface
// the configured methods, headers, and Max-Age so the browser caches
// the result and skips repeating the preflight on every API call.
func TestBuildCORS_PreflightAdvertisesMethodsHeadersAndMaxAge(t *testing.T) {
	t.Parallel()
	e := newCORSEchoForTest(t, "https://allowed.example")

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/links", nil)
	req.Header.Set(echo.HeaderOrigin, "https://allowed.example")
	req.Header.Set(echo.HeaderAccessControlRequestMethod, http.MethodPost)
	req.Header.Set(echo.HeaderAccessControlRequestHeaders, "Content-Type")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	// Preflight should short-circuit at 204 (Echo's default) without
	// ever invoking the wrapped POST handler.
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get(echo.HeaderAccessControlAllowMethods); got == "" {
		t.Error("ACAM header missing on preflight")
	}
	if got := rec.Header().Get(echo.HeaderAccessControlAllowHeaders); got == "" {
		t.Error("ACAH header missing on preflight")
	}
	if got := rec.Header().Get(echo.HeaderAccessControlMaxAge); got == "" {
		t.Error("Access-Control-Max-Age header missing on preflight")
	}
}
