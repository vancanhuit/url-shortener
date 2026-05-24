package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
)

// newSecureEchoForTest wires the secure-headers middleware onto a stub
// handler returning 200 so the response headers are observable.
func newSecureEchoForTest(t *testing.T) *echo.Echo {
	t.Helper()
	e := echo.New()
	e.Use(buildSecureHeaders())
	e.GET("/", func(c *echo.Context) error { return c.NoContent(http.StatusOK) })
	return e
}

func doPlainRequest(t *testing.T, e *echo.Echo) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// TestSecureHeaders_XContentTypeOptions verifies that every response
// carries X-Content-Type-Options: nosniff to prevent MIME-type sniffing.
func TestSecureHeaders_XContentTypeOptions(t *testing.T) {
	t.Parallel()
	rec := doPlainRequest(t, newSecureEchoForTest(t))
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
}

// TestSecureHeaders_XFrameOptions verifies that every response carries
// X-Frame-Options: SAMEORIGIN to defend against clickjacking.
func TestSecureHeaders_XFrameOptions(t *testing.T) {
	t.Parallel()
	rec := doPlainRequest(t, newSecureEchoForTest(t))
	if got := rec.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q, want %q", got, "SAMEORIGIN")
	}
}

// TestSecureHeaders_ReferrerPolicy verifies the Referrer-Policy header.
func TestSecureHeaders_ReferrerPolicy(t *testing.T) {
	t.Parallel()
	rec := doPlainRequest(t, newSecureEchoForTest(t))
	want := "strict-origin-when-cross-origin"
	if got := rec.Header().Get("Referrer-Policy"); got != want {
		t.Errorf("Referrer-Policy = %q, want %q", got, want)
	}
}

// TestSecureHeaders_NoHSTSOverPlainHTTP verifies that
// Strict-Transport-Security is NOT emitted for plain HTTP requests;
// Echo only sets it when the request is TLS or X-Forwarded-Proto: https.
func TestSecureHeaders_NoHSTSOverPlainHTTP(t *testing.T) {
	t.Parallel()
	rec := doPlainRequest(t, newSecureEchoForTest(t))
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("Strict-Transport-Security should be empty for HTTP, got %q", got)
	}
}

// TestSecureHeaders_HSTSViaXForwardedProto verifies that
// Strict-Transport-Security IS emitted when the upstream proxy signals
// HTTPS via X-Forwarded-Proto.
func TestSecureHeaders_HSTSViaXForwardedProto(t *testing.T) {
	t.Parallel()
	e := newSecureEchoForTest(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	hsts := rec.Header().Get("Strict-Transport-Security")
	if hsts == "" {
		t.Fatalf("Strict-Transport-Security missing for X-Forwarded-Proto: https")
	}
	wantPrefix := "max-age=63072000"
	if len(hsts) < len(wantPrefix) || hsts[:len(wantPrefix)] != wantPrefix {
		t.Errorf("Strict-Transport-Security = %q, want prefix %q", hsts, wantPrefix)
	}
}
