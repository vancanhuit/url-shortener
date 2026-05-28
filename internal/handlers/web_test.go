package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"

	"github.com/vancanhuit/url-shortener/internal/handlers"
)

// fakeDist builds a fs.FS with the same shape `web/dist/` produces:
// `index.html` at the root plus `assets/` and `static/` subdirectories.
// The Vite + Svelte refactor expects exactly this layout, so an SPA
// handler test that mirrors it catches drift early.
func fakeDist() fstest.MapFS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte("<!doctype html><html><head><title>URL Shortener</title></head><body>spa</body></html>"),
		},
		"assets/index-abc123.js":     &fstest.MapFile{Data: []byte("console.log('app')")},
		"assets/index-abc123.css":    &fstest.MapFile{Data: []byte("body{margin:0}")},
		"static/swagger-ui.css":      &fstest.MapFile{Data: []byte(":root{}")},
		"static/redoc.standalone.js": &fstest.MapFile{Data: []byte("window.Redoc = {}")},
	}
}

func newSPARouter(t *testing.T) chi.Router {
	t.Helper()
	dist := fakeDist()
	idx, err := dist.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	spa := handlers.NewSPA(handlers.SPAConfig{
		DistFS:    dist,
		IndexHTML: idx,
	})
	r := chi.NewRouter()
	spa.Mount(r)
	return r
}

func TestSPA_IndexServesEmbeddedHTMLWithNoCacheHeaders(t *testing.T) {
	t.Parallel()
	r := newSPARouter(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<title>URL Shortener</title>") {
		t.Errorf("body should contain SPA title, got %q", body)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache (so a deploy is reflected on the next nav)", got)
	}
	if rec.Header().Get("Last-Modified") == "" {
		t.Error("Last-Modified should be set")
	}
}

func TestSPA_HashedAssetsAreServedAsImmutable(t *testing.T) {
	t.Parallel()
	r := newSPARouter(t)

	for _, path := range []string{"/assets/index-abc123.js", "/assets/index-abc123.css"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, rec.Code)
		}
		// Hashed bundles include a content hash in the filename, so
		// the contract is to cache them indefinitely on a CDN /
		// browser. Anything weaker forces a redundant revalidate on
		// every navigation.
		if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
			t.Errorf("%s Cache-Control = %q, want immutable", path, got)
		}
	}
}

func TestSPA_StaticVendoredAssetsAreServedWithRevalidate(t *testing.T) {
	t.Parallel()
	r := newSPARouter(t)

	for _, path := range []string{"/static/swagger-ui.css", "/static/redoc.standalone.js"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, rec.Code)
		}
		// Vendored assets are NOT content-hashed (they're copied
		// verbatim from npm), so we need must-revalidate to let a
		// release upgrade propagate within the max-age window.
		if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "must-revalidate") {
			t.Errorf("%s Cache-Control = %q, want must-revalidate", path, got)
		}
	}
}

func TestSPA_UnknownAssetReturns404(t *testing.T) {
	t.Parallel()
	r := newSPARouter(t)

	req := httptest.NewRequest(http.MethodGet, "/assets/nope.js", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
