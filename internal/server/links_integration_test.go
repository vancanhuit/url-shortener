//go:build integration

// End-to-end integration test for the JSON link API and the public
// redirect handler. Brings up the full server (with real Store, Cache, and
// Generator) on a random port and drives it over the network.

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/vancanhuit/url-shortener/internal/cache"
	"github.com/vancanhuit/url-shortener/internal/config"
	"github.com/vancanhuit/url-shortener/internal/server"
	"github.com/vancanhuit/url-shortener/internal/shortener"
	"github.com/vancanhuit/url-shortener/internal/store"
)

// startFullServer spins up a Server with a real Postgres, Redis, and code
// generator. It skips the test when the URL_SHORTENER_TEST_* env vars are
// unset (matching the behaviour of the per-package integration tests).
func startFullServer(t *testing.T) (string, func()) {
	t.Helper()

	dbURL := os.Getenv("URL_SHORTENER_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("URL_SHORTENER_TEST_DATABASE_URL not set; skipping integration test")
	}
	redisURL := os.Getenv("URL_SHORTENER_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("URL_SHORTENER_TEST_REDIS_URL not set; skipping integration test")
	}

	ctx := t.Context()
	st, err := store.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(st.Close)

	cc, err := cache.New(ctx, redisURL)
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })

	gen, err := shortener.NewGenerator(shortener.DefaultLength)
	if err != nil {
		t.Fatalf("shortener.NewGenerator: %v", err)
	}

	cfg := config.Config{
		Env:        config.EnvDev,
		Addr:       "127.0.0.1:0",
		BaseURL:    "http://short.test",
		LogLevel:   "info",
		LogFormat:  "text",
		CodeLength: shortener.DefaultLength,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := server.New(cfg, logger, server.Deps{Store: st, Cache: cc, Generator: gen})

	runCtx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(runCtx, ln) }()

	stop := func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve returned error: %v", err)
			}
		case <-time.After(20 * time.Second):
			t.Error("Serve did not return within 20s of cancellation")
		}
	}

	waitForReady(t, "http://"+ln.Addr().String()+"/healthz")
	return "http://" + ln.Addr().String(), stop
}

// httpClientNoRedirect is a non-following client so we can inspect 302s.
var httpClientNoRedirect = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
	Timeout: 5 * time.Second,
}

func TestLinksAPI_CreateGetRedirectFlow(t *testing.T) {
	base, stop := startFullServer(t)
	defer stop()

	// 1. Create a link with an auto-generated code.
	target := "https://example.com/integration/" + randomSuffix(t)
	body, _ := json.Marshal(map[string]string{"target_url": target})
	req, _ := http.NewRequestWithContext(t.Context(),
		http.MethodPost, base+"/api/v1/links", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST status = %d", resp.StatusCode)
	}
	var created struct {
		Code      string `json:"code"`
		ShortURL  string `json:"short_url"`
		TargetURL string `json:"target_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !shortener.ValidCode(created.Code) {
		t.Errorf("Code %q is not valid", created.Code)
	}
	if created.TargetURL != target {
		t.Errorf("TargetURL = %q, want %q", created.TargetURL, target)
	}
	if created.ShortURL != "http://short.test/r/"+created.Code {
		t.Errorf("ShortURL = %q", created.ShortURL)
	}

	// 2. Fetch it back via the API.
	resp, err = http.Get(base + "/api/v1/links/" + created.Code) //nolint:noctx,gosec // test helper
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d", resp.StatusCode)
	}
	var fetched struct {
		Code      string `json:"code"`
		TargetURL string `json:"target_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&fetched); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if fetched.Code != created.Code || fetched.TargetURL != target {
		t.Errorf("fetched = %+v", fetched)
	}

	// 3. Hit the public redirect; expect 302 to the target URL.
	rreq, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/r/"+created.Code, nil)
	rresp, err := httpClientNoRedirect.Do(rreq)
	if err != nil {
		t.Fatalf("redirect: %v", err)
	}
	_ = rresp.Body.Close()
	if rresp.StatusCode != http.StatusFound {
		t.Fatalf("redirect status = %d", rresp.StatusCode)
	}
	if loc := rresp.Header.Get("Location"); loc != target {
		t.Errorf("Location = %q, want %q", loc, target)
	}

	// 4. /readyz should report both postgres and redis healthy.
	code, body2 := getJSON(t, base+"/readyz")
	if code != http.StatusOK {
		t.Errorf("/readyz status = %d", code)
	}
	if body2["postgres"] != "ok" || body2["redis"] != "ok" {
		t.Errorf("/readyz body = %v", body2)
	}
}

func TestLinksAPI_UnknownCodeReturns404(t *testing.T) {
	base, stop := startFullServer(t)
	defer stop()

	resp, err := http.Get(base + "/api/v1/links/zzzzzzz") //nolint:noctx,gosec // test helper
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}

	rreq, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/r/zzzzzzz", nil)
	rresp, err := httpClientNoRedirect.Do(rreq)
	if err != nil {
		t.Fatalf("redirect: %v", err)
	}
	_ = rresp.Body.Close()
	if rresp.StatusCode != http.StatusNotFound {
		t.Errorf("redirect status = %d, want 404", rresp.StatusCode)
	}
}

// TestServer_GracefulShutdownDrainsBackgroundTasks proves that a click
// fired by a request right before SIGTERM is committed before Serve
// returns, rather than dropped along with the process. Setup is
// inlined (rather than reusing startFullServer) because the assertion
// has to read the underlying store *after* the server has stopped,
// which startFullServer's t.Cleanup ordering doesn't expose cleanly.
func TestServer_GracefulShutdownDrainsBackgroundTasks(t *testing.T) {
	dbURL := os.Getenv("URL_SHORTENER_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("URL_SHORTENER_TEST_DATABASE_URL not set; skipping integration test")
	}
	redisURL := os.Getenv("URL_SHORTENER_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("URL_SHORTENER_TEST_REDIS_URL not set; skipping integration test")
	}

	ctx := t.Context()
	st, err := store.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	cc, err := cache.New(ctx, redisURL)
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	defer func() { _ = cc.Close() }()

	gen, err := shortener.NewGenerator(shortener.DefaultLength)
	if err != nil {
		t.Fatalf("shortener.NewGenerator: %v", err)
	}

	cfg := config.Config{
		Env:        config.EnvDev,
		Addr:       "127.0.0.1:0",
		BaseURL:    "http://short.test",
		LogLevel:   "info",
		LogFormat:  "text",
		CodeLength: shortener.DefaultLength,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := server.New(cfg, logger, server.Deps{Store: st, Cache: cc, Generator: gen})

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(runCtx, ln) }()

	base := "http://" + ln.Addr().String()
	waitForReady(t, base+"/healthz")

	// Seed a link so the redirect has somewhere to point. CreateLink
	// inserts directly to skip the API layer (and its async paths).
	target := "https://example.com/drain/" + randomSuffix(t)
	link, err := st.CreateLink(ctx, nil, "drn"+randomSuffix(t)[:4], target, nil)
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	// Fire the redirect. The handler returns 302 immediately and the
	// click counter increment runs on a background goroutine that
	// the server's drain logic must wait for.
	rreq, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/r/"+link.Code, nil)
	rresp, err := httpClientNoRedirect.Do(rreq)
	if err != nil {
		t.Fatalf("redirect: %v", err)
	}
	_ = rresp.Body.Close()
	if rresp.StatusCode != http.StatusFound {
		t.Fatalf("redirect status = %d", rresp.StatusCode)
	}

	// Trigger graceful shutdown without giving the goroutine any
	// extra wall-clock time first; if the drain works we still see
	// click_count=1 below.
	cancel()
	select {
	case serveErr := <-done:
		if serveErr != nil {
			t.Fatalf("Serve returned error: %v", serveErr)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Serve did not return within 20s of cancellation")
	}

	got, err := st.GetLinkByCode(ctx, nil, link.Code)
	if err != nil {
		t.Fatalf("GetLinkByCode: %v", err)
	}
	if got.ClickCount != 1 {
		t.Errorf("ClickCount = %d after shutdown, want 1 -- drain dropped the increment",
			got.ClickCount)
	}
}

// randomSuffix is a tiny per-test marker so concurrent runs can't share a
// target URL. Hex of a fresh code is good enough.
func randomSuffix(t *testing.T) string {
	t.Helper()
	g, err := shortener.NewGenerator(shortener.MaxLength)
	if err != nil {
		t.Fatalf("rng: %v", err)
	}
	s, err := g.Generate()
	if err != nil {
		t.Fatalf("rng: %v", err)
	}
	return s
}
