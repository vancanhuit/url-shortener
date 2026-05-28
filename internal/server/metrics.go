package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// durationBuckets are the histogram bucket boundaries (seconds).
var durationBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

// newMetricsRegistry returns a Prometheus registry pre-populated with
// the standard Go runtime and process collectors.
func newMetricsRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return reg
}

// newRequestMetrics allocates the RED metric vectors and registers them with reg.
func newRequestMetrics(reg *prometheus.Registry) (*prometheus.CounterVec, *prometheus.HistogramVec) {
	requestsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests partitioned by method, route template, and status code.",
	}, []string{"method", "route", "status_code"})

	requestDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request duration in seconds partitioned by method and route template.",
		Buckets: durationBuckets,
	}, []string{"method", "route"})

	reg.MustRegister(requestsTotal, requestDuration)
	return requestsTotal, requestDuration
}

// statusRecorder wraps http.ResponseWriter to capture the written status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// buildMetricsMiddleware returns a Chi-compatible middleware that records RED metrics.
func buildMetricsMiddleware(
	requestsTotal *prometheus.CounterVec,
	requestDuration *prometheus.HistogramVec,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			dur := time.Since(start).Seconds()

			route := ""
			if rctx := chi.RouteContext(r.Context()); rctx != nil {
				route = rctx.RoutePattern()
			}
			if route == "" {
				route = "unmatched"
			}
			method := r.Method
			code := strconv.Itoa(rec.status)

			requestsTotal.WithLabelValues(method, route, code).Inc()
			requestDuration.WithLabelValues(method, route).Observe(dur)
		})
	}
}

// mountMetrics registers the GET /metrics endpoint backed by reg.
func mountMetrics(r chi.Router, reg *prometheus.Registry) {
	h := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	r.Get("/metrics", h.ServeHTTP)
}
