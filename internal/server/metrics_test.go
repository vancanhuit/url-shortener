package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// newMetricsRouterForTest wires a fresh metrics middleware and an
// isolated registry onto a minimal Chi router and registers the
// routes given by the caller.
func newMetricsRouterForTest(
	t *testing.T,
	routes map[string]int, // path -> response status
) (chi.Router, *prometheus.CounterVec, *prometheus.HistogramVec) {
	t.Helper()
	reg := prometheus.NewRegistry()
	total, dur := newRequestMetrics(reg)

	r := chi.NewRouter()
	r.Use(buildMetricsMiddleware(total, dur))
	for path, status := range routes {
		s := status
		r.Get(path, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(s)
		})
	}
	return r, total, dur
}

// counterValue extracts the current value of a CounterVec cell
// identified by its label values.
func counterValue(t *testing.T, cv *prometheus.CounterVec, lvs ...string) float64 {
	t.Helper()
	m := &dto.Metric{}
	c, err := cv.GetMetricWithLabelValues(lvs...)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues(%v): %v", lvs, err)
	}
	if err := c.Write(m); err != nil {
		t.Fatalf("Write metric: %v", err)
	}
	return m.GetCounter().GetValue()
}

// TestMetricsMiddleware_CountsRequests verifies that each request
// increments http_requests_total by one with the correct labels.
func TestMetricsMiddleware_CountsRequests(t *testing.T) {
	t.Parallel()
	r, total, _ := newMetricsRouterForTest(t, map[string]int{
		"/healthz": http.StatusOK,
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := counterValue(t, total, "GET", "/healthz", "200"); got != 1 {
		t.Errorf("http_requests_total{GET,/healthz,200} = %v, want 1", got)
	}
}

// TestMetricsMiddleware_LabelsUnmatchedRoute verifies that requests
// to unknown paths get the "unmatched" route label instead of the raw
// URL (which would be unbounded-cardinality).
func TestMetricsMiddleware_LabelsUnmatchedRoute(t *testing.T) {
	t.Parallel()
	// Register a real route so chi builds its handler (without at least one
	// route, chi bypasses the middleware chain for 404s entirely).
	r, total, _ := newMetricsRouterForTest(t, map[string]int{
		"/some-known-path": http.StatusOK,
	})

	req := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if got := counterValue(t, total, "GET", "unmatched", "404"); got != 1 {
		t.Errorf("http_requests_total{GET,unmatched,404} = %v, want 1", got)
	}
}

// TestMetricsMiddleware_RecordsErrorStatusCode verifies that a handler
// returning a non-2xx status code is labeled with that code.
func TestMetricsMiddleware_RecordsErrorStatusCode(t *testing.T) {
	t.Parallel()
	r, total, _ := newMetricsRouterForTest(t, map[string]int{
		"/boom": http.StatusInternalServerError,
	})

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if got := counterValue(t, total, "GET", "/boom", "500"); got != 1 {
		t.Errorf("http_requests_total{GET,/boom,500} = %v, want 1", got)
	}
}

// TestMetricsMiddleware_RecordsDuration verifies that the histogram
// has at least one observation for a completed request.
func TestMetricsMiddleware_RecordsDuration(t *testing.T) {
	t.Parallel()
	r, _, dur := newMetricsRouterForTest(t, map[string]int{
		"/ping": http.StatusOK,
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	h, err := dur.GetMetricWithLabelValues("GET", "/ping")
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues: %v", err)
	}
	m := &dto.Metric{}
	hm, ok := h.(prometheus.Metric)
	if !ok {
		t.Fatal("histogram observer does not implement prometheus.Metric")
	}
	if err := hm.Write(m); err != nil {
		t.Fatalf("Write metric: %v", err)
	}
	if count := m.GetHistogram().GetSampleCount(); count != 1 {
		t.Errorf("histogram sample count = %d, want 1", count)
	}
}

// TestMetricsEndpoint_Returns200 verifies that GET /metrics returns
// 200 with Prometheus text content.
func TestMetricsEndpoint_Returns200(t *testing.T) {
	t.Parallel()
	reg := newMetricsRegistry()
	r := chi.NewRouter()
	mountMetrics(r, reg)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain prefix", ct)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "go_goroutines") {
		t.Errorf("metrics body does not contain go_goroutines")
	}
}
