package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// durationBuckets are the histogram bucket boundaries (seconds) for
// http_request_duration_seconds. They cover fast cache hits (~5 ms)
// through slow DB-backed requests (~5 s) with a 10 s catch-all.
var durationBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

// newMetricsRegistry returns a Prometheus registry pre-populated with
// the standard Go runtime and process collectors. A custom registry is
// used instead of the global default so that multiple server
// instantiations in tests don't trigger double-registration panics.
func newMetricsRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return reg
}

// newRequestMetrics allocates the RED metric vectors and registers them
// with reg. They are returned separately so callers can inject them
// into the middleware and into tests.
//
//   - http_requests_total{method,route,status_code} — request rate / errors
//   - http_request_duration_seconds{method,route}   — latency distribution
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

// buildMetricsMiddleware returns an Echo middleware that records RED
// (Rate, Errors, Duration) metrics for every HTTP request.
//
// The "route" label uses the matched route template (e.g. "/:code")
// so cardinality stays bounded even under high-entropy path segments.
// Requests that don't match any registered route are labeled
// "unmatched".
func buildMetricsMiddleware(
	requestsTotal *prometheus.CounterVec,
	requestDuration *prometheus.HistogramVec,
) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			start := time.Now()
			err := next(c)
			dur := time.Since(start).Seconds()

			route := c.Path()
			if route == "" {
				route = "unmatched"
			}
			method := c.Request().Method
			_, status := echo.ResolveResponseStatus(c.Response(), err)
			if status == 0 {
				status = http.StatusOK
			}
			code := strconv.Itoa(status)

			requestsTotal.WithLabelValues(method, route, code).Inc()
			requestDuration.WithLabelValues(method, route).Observe(dur)

			return err
		}
	}
}

// mountMetrics registers the GET /metrics endpoint backed by reg.
// The handler is the standard promhttp text exposition format so any
// Prometheus scraper can consume it out of the box.
func mountMetrics(e *echo.Echo, reg *prometheus.Registry) {
	h := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	e.GET("/metrics", func(c *echo.Context) error {
		h.ServeHTTP(c.Response(), c.Request())
		return nil
	})
}
