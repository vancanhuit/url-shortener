package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/vancanhuit/url-shortener/internal/handlers"
)

// fakeMetrics is a concurrency-safe handlers.Metrics that records every
// observation so tests can assert the handler emits the right outcome.
type fakeMetrics struct {
	mu         sync.Mutex
	shorten    map[handlers.ShortenOutcome]int
	redirect   map[handlers.RedirectOutcome]int
	collisions int
}

func newFakeMetrics() *fakeMetrics {
	return &fakeMetrics{
		shorten:  map[handlers.ShortenOutcome]int{},
		redirect: map[handlers.RedirectOutcome]int{},
	}
}

func (f *fakeMetrics) IncShorten(o handlers.ShortenOutcome) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shorten[o]++
}

func (f *fakeMetrics) IncRedirect(o handlers.RedirectOutcome) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.redirect[o]++
}

func (f *fakeMetrics) IncCodeCollision() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.collisions++
}

func (f *fakeMetrics) shortenCount(o handlers.ShortenOutcome) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shorten[o]
}

func (f *fakeMetrics) redirectCount(o handlers.RedirectOutcome) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.redirect[o]
}

func newHandlerWithMetrics(
	t *testing.T,
	st handlers.LinkStore,
	cc handlers.LinkCache,
	gen handlers.Generator,
	m handlers.Metrics,
) chi.Router {
	t.Helper()
	h := handlers.NewLinks(handlers.LinksConfig{
		Store:     st,
		Cache:     cc,
		Generator: gen,
		BaseURL:   baseURL,
		Metrics:   m,
	})
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

// TestMetrics_ShortenCreated: a fresh create increments the "created"
// shorten counter.
func TestMetrics_ShortenCreated(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	m := newFakeMetrics()
	e := newHandlerWithMetrics(t, st, cc, &scriptedGen{codes: []string{"abc1234"}}, m)

	rec, body := doJSON(t, e, http.MethodPost, "/api/v1/links", `{"target_url":"https://example.com"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, string(body))
	}
	if got := m.shortenCount(handlers.ShortenCreated); got != 1 {
		t.Errorf("ShortenCreated = %d, want 1", got)
	}
}

// TestMetrics_ShortenDeduped: a second auto-generated create for the
// same target reuses the row and increments the "deduped" counter.
func TestMetrics_ShortenDeduped(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	m := newFakeMetrics()
	e := newHandlerWithMetrics(t, st, cc, &scriptedGen{codes: []string{"first00", "second0"}}, m)

	const reqBody = `{"target_url":"https://dedup.example"}`
	if rec, body := doJSON(t, e, http.MethodPost, "/api/v1/links", reqBody); rec.Code != http.StatusCreated {
		t.Fatalf("first create status = %d, body = %s", rec.Code, string(body))
	}
	rec, body := doJSON(t, e, http.MethodPost, "/api/v1/links", reqBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("second create status = %d, body = %s (want 200 dedup)", rec.Code, string(body))
	}
	if got := m.shortenCount(handlers.ShortenCreated); got != 1 {
		t.Errorf("ShortenCreated = %d, want 1", got)
	}
	if got := m.shortenCount(handlers.ShortenDeduped); got != 1 {
		t.Errorf("ShortenDeduped = %d, want 1", got)
	}
}

// TestMetrics_RedirectStoreHitThenCacheHit: the first redirect resolves
// from the store, the second from the cache, each recording its outcome.
func TestMetrics_RedirectStoreHitThenCacheHit(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	if _, err := st.CreateLink(context.Background(), nil, "live123", "https://example.com", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m := newFakeMetrics()
	e := newHandlerWithMetrics(t, st, cc, &scriptedGen{}, m)

	do := func() int {
		req := httptest.NewRequest(http.MethodGet, "/r/live123", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := do(); c != http.StatusFound {
		t.Fatalf("first redirect = %d, want 302", c)
	}
	if c := do(); c != http.StatusFound {
		t.Fatalf("second redirect = %d, want 302", c)
	}
	if got := m.redirectCount(handlers.RedirectStoreHit); got != 1 {
		t.Errorf("RedirectStoreHit = %d, want 1", got)
	}
	if got := m.redirectCount(handlers.RedirectCacheHit); got != 1 {
		t.Errorf("RedirectCacheHit = %d, want 1", got)
	}
}

// TestMetrics_RedirectNotFound: an unknown code records a not_found
// outcome (and the negative-cache path records negative_hit on repeat).
func TestMetrics_RedirectNotFound(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	m := newFakeMetrics()
	e := newHandlerWithMetrics(t, st, cc, &scriptedGen{}, m)

	do := func() int {
		req := httptest.NewRequest(http.MethodGet, "/r/missing", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := do(); c != http.StatusNotFound {
		t.Fatalf("first redirect = %d, want 404", c)
	}
	if c := do(); c != http.StatusNotFound {
		t.Fatalf("second redirect = %d, want 404", c)
	}
	if got := m.redirectCount(handlers.RedirectNotFound); got != 1 {
		t.Errorf("RedirectNotFound = %d, want 1", got)
	}
	if got := m.redirectCount(handlers.RedirectNegativeHit); got != 1 {
		t.Errorf("RedirectNegativeHit = %d, want 1", got)
	}
}

// TestMetrics_NilMetricsIsNoop: constructing without a Metrics must not
// panic when a handler path fires an observation (the default no-op).
func TestMetrics_NilMetricsIsNoop(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	e := newHandlerWithMetrics(t, st, cc, &scriptedGen{codes: []string{"noop123"}}, nil)

	rec, body := doJSON(t, e, http.MethodPost, "/api/v1/links",
		`{"target_url":"https://example.com"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, string(body))
	}
}
