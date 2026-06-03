package server

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/vancanhuit/url-shortener/internal/handlers"
)

// gatherCounter returns the value of the named counter, optionally
// filtered to the series carrying the given label values, by gathering
// from reg. It returns 0 when the series is absent.
func gatherCounter(t *testing.T, reg *prometheus.Registry, name string, wantLabels map[string]string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if metricMatchesLabels(m, wantLabels) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func metricMatchesLabels(m *dto.Metric, want map[string]string) bool {
	for k, v := range want {
		found := false
		for _, lp := range m.GetLabel() {
			if lp.GetName() == k && lp.GetValue() == v {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// TestBusinessMetrics_PreInitializesSeries verifies the known label
// series export at 0 before any observation, so dashboards don't show
// gaps for outcomes that haven't happened yet.
func TestBusinessMetrics_PreInitializesSeries(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	newBusinessMetrics(reg)

	for _, o := range []handlers.ShortenOutcome{handlers.ShortenCreated, handlers.ShortenDeduped} {
		if v := gatherCounter(t, reg, "links_shortened_total", map[string]string{"outcome": string(o)}); v != 0 {
			t.Errorf("links_shortened_total{outcome=%q} = %v, want 0", o, v)
		}
	}
	for _, o := range []handlers.RedirectOutcome{
		handlers.RedirectCacheHit, handlers.RedirectNegativeHit, handlers.RedirectStoreHit,
		handlers.RedirectNotFound, handlers.RedirectGone, handlers.RedirectError,
	} {
		if v := gatherCounter(t, reg, "links_redirects_total", map[string]string{"outcome": string(o)}); v != 0 {
			t.Errorf("links_redirects_total{outcome=%q} = %v, want 0", o, v)
		}
	}
}

// TestBusinessMetrics_Increments verifies each Inc* method advances the
// right series, satisfying the handlers.Metrics contract.
func TestBusinessMetrics_Increments(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	var m handlers.Metrics = newBusinessMetrics(reg)

	m.IncShorten(handlers.ShortenCreated)
	m.IncShorten(handlers.ShortenCreated)
	m.IncShorten(handlers.ShortenDeduped)
	m.IncRedirect(handlers.RedirectStoreHit)
	m.IncRedirect(handlers.RedirectGone)
	m.IncCodeCollision()

	if v := gatherCounter(t, reg, "links_shortened_total", map[string]string{"outcome": "created"}); v != 2 {
		t.Errorf("shortened created = %v, want 2", v)
	}
	if v := gatherCounter(t, reg, "links_shortened_total", map[string]string{"outcome": "deduped"}); v != 1 {
		t.Errorf("shortened deduped = %v, want 1", v)
	}
	if v := gatherCounter(t, reg, "links_redirects_total", map[string]string{"outcome": "store_hit"}); v != 1 {
		t.Errorf("redirect store_hit = %v, want 1", v)
	}
	if v := gatherCounter(t, reg, "links_redirects_total", map[string]string{"outcome": "gone"}); v != 1 {
		t.Errorf("redirect gone = %v, want 1", v)
	}
	if v := gatherCounter(t, reg, "links_code_collisions_total", nil); v != 1 {
		t.Errorf("code collisions = %v, want 1", v)
	}
}

// TestBusinessMetrics_RateLimited verifies the middleware-side rejection
// counter advances independently of the handlers.Metrics surface.
func TestBusinessMetrics_RateLimited(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	bm := newBusinessMetrics(reg)

	bm.incRateLimited()
	bm.incRateLimited()

	if v := gatherCounter(t, reg, "links_rate_limited_total", nil); v != 2 {
		t.Errorf("rate_limited = %v, want 2", v)
	}
}
