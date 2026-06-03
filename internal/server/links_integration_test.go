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
	"strings"
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
// unset (matching the behavior of the per-package integration tests).
//
// Most callers only need the base URL + stop func; tests that need to
// poke the underlying dependencies (e.g. simulate an outage by closing
// them mid-test) should call startFullServerWithDeps instead.
func startFullServer(t *testing.T) (string, func()) {
	t.Helper()
	base, _, _, stop := startFullServerWithDeps(t)
	return base, stop
}

// startFullServerWithDeps is startFullServer plus the live *store.Store
// and *cache.Client driving the server. Returning them lets a test
// simulate runtime dependency failure by calling Close on the dep --
// subsequent /readyz Pings will fail and the handler should flip to 503.
//
// The returned deps are still owned by the helper: their Close is
// already registered via t.Cleanup so calling it early in a test is
// safe (both Close methods are no-ops or error-ignored on the second
// call from cleanup).
func startFullServerWithDeps(t *testing.T) (string, *store.Store, *cache.Client, func()) {
	t.Helper()
	base, st, cc, _, stop := startFullServerInternal(t)
	return base, st, cc, stop
}

// startFullServerInternal is the shared implementation behind
// startFullServer / startFullServerWithDeps. It additionally returns the
// net.Listener the server is bound to, which the graceful-shutdown test
// uses to assert the listener is genuinely closed after Serve returns --
// a check that's immune to the ephemeral-port reuse races a fresh dial
// would be subject to.
func startFullServerInternal(t *testing.T) (string, *store.Store, *cache.Client, net.Listener, func()) {
	t.Helper()

	dbURL := os.Getenv("URL_SHORTENER_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Fatal("URL_SHORTENER_TEST_DATABASE_URL must be set to run integration tests")
	}
	redisURL := os.Getenv("URL_SHORTENER_TEST_REDIS_URL")
	if redisURL == "" {
		t.Fatal("URL_SHORTENER_TEST_REDIS_URL must be set to run integration tests")
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

	waitForReady(t, "http://"+ln.Addr().String()+"/livez")
	return "http://" + ln.Addr().String(), st, cc, ln, stop
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
	resp, err = http.Get(base + "/api/v1/links/" + created.Code)
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

// TestLinksAPI_DeleteFlow walks the soft-delete lifecycle through the
// real HTTP server, real Postgres, real Redis -- the same wires the
// production stack uses. The flow is:
//
//  1. POST creates a link, populating the cache via the create path.
//  2. /r/:code redirects (302) and lands a cached entry.
//  3. DELETE returns 204; the cache is invalidated server-side.
//  4. /r/:code now returns 410 (cache miss + tombstoned row).
//  5. /api/v1/links/:code returns 410 with `code=link_deleted`,
//     letting programmatic clients distinguish a retired link from
//     an unknown one.
//  6. A second DELETE returns 404 -- documents the API's
//     semantically-idempotent-but-response-distinct behavior.
//
// No unit-test seam exercises the cache-invalidation path against a
// real Redis, so this test is the safety net for that interaction.
func TestLinksAPI_DeleteFlow(t *testing.T) {
	base, stop := startFullServer(t)
	defer stop()

	// 1. Create.
	target := "https://example.com/integration/delete/" + randomSuffix(t)
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
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// 2. Warm the cache via /r so step 4 must observe an explicit
	// invalidation rather than a serendipitous miss.
	rreq, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/r/"+created.Code, nil)
	rresp, err := httpClientNoRedirect.Do(rreq)
	if err != nil {
		t.Fatalf("warmup redirect: %v", err)
	}
	_ = rresp.Body.Close()
	if rresp.StatusCode != http.StatusFound {
		t.Fatalf("warmup redirect status = %d", rresp.StatusCode)
	}

	// 3. DELETE.
	dreq, _ := http.NewRequestWithContext(t.Context(),
		http.MethodDelete, base+"/api/v1/links/"+created.Code, nil)
	dresp, err := http.DefaultClient.Do(dreq)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	_ = dresp.Body.Close()
	if dresp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", dresp.StatusCode)
	}

	// 4. /r/:code -> 410 (cache invalidation + tombstoned row).
	rreq, _ = http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/r/"+created.Code, nil)
	rresp, err = httpClientNoRedirect.Do(rreq)
	if err != nil {
		t.Fatalf("post-delete redirect: %v", err)
	}
	_ = rresp.Body.Close()
	if rresp.StatusCode != http.StatusGone {
		t.Errorf("redirect status = %d, want 410", rresp.StatusCode)
	}

	// 5. /api/v1/links/:code -> 410 + link_deleted.
	gresp, err := http.Get(base + "/api/v1/links/" + created.Code)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = gresp.Body.Close() }()
	if gresp.StatusCode != http.StatusGone {
		t.Errorf("GET status = %d, want 410", gresp.StatusCode)
	}
	var errBody struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(gresp.Body).Decode(&errBody); err != nil {
		t.Fatalf("decode err body: %v", err)
	}
	if errBody.Code != "link_deleted" {
		t.Errorf("error code = %q, want link_deleted", errBody.Code)
	}

	// 6. Second DELETE -> 404 (semantically idempotent, response
	//    distinct: see Delete handler doc-comment).
	dreq, _ = http.NewRequestWithContext(t.Context(),
		http.MethodDelete, base+"/api/v1/links/"+created.Code, nil)
	dresp, err = http.DefaultClient.Do(dreq)
	if err != nil {
		t.Fatalf("second DELETE: %v", err)
	}
	_ = dresp.Body.Close()
	if dresp.StatusCode != http.StatusNotFound {
		t.Errorf("second DELETE status = %d, want 404", dresp.StatusCode)
	}
}

// TestServer_ServesEmbeddedOpenAPISpec proves that
// `GET /api/v1/openapi.{json,yaml}` is wired into the live server and
// returns the spec embedded at build time. Unit tests already cover
// the handler-layer behavior in isolation (`MountOpenAPI`); this test
// is the wiring check that catches a missing call from `server.New`.
func TestServer_ServesEmbeddedOpenAPISpec(t *testing.T) {
	base, stop := startFullServer(t)
	defer stop()

	resp, err := http.Get(base + "/api/v1/openapi.json")
	if err != nil {
		t.Fatalf("GET openapi.json: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json...", ct)
	}
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc["openapi"] != "3.0.3" {
		t.Errorf("openapi = %v, want 3.0.3", doc["openapi"])
	}
	// Cross-check: the served spec must declare the DELETE op
	// landed in the previous PR. If it doesn't, the spec drifted
	// out of sync with the code.
	paths, _ := doc["paths"].(map[string]any)
	link, _ := paths["/api/v1/links/{code}"].(map[string]any)
	if _, ok := link["delete"]; !ok {
		t.Error("served spec is missing DELETE /api/v1/links/{code}")
	}
}

func TestLinksAPI_UnknownCodeReturns404(t *testing.T) {
	base, stop := startFullServer(t)
	defer stop()

	resp, err := http.Get(base + "/api/v1/links/zzzzzzz")
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
		t.Fatal("URL_SHORTENER_TEST_DATABASE_URL must be set to run integration tests")
	}
	redisURL := os.Getenv("URL_SHORTENER_TEST_REDIS_URL")
	if redisURL == "" {
		t.Fatal("URL_SHORTENER_TEST_REDIS_URL must be set to run integration tests")
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
	waitForReady(t, base+"/livez")

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
