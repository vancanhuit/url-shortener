package server

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/vancanhuit/url-shortener/internal/handlers"
)

// businessMetrics is the Prometheus-backed implementation of
// handlers.Metrics plus the rate-limit rejection counter. It is held by
// the server so the same registry exposes RED, Go runtime, pool, and
// business metrics from a single /metrics endpoint.
type businessMetrics struct {
	shorten        *prometheus.CounterVec
	redirect       *prometheus.CounterVec
	codeCollisions prometheus.Counter
	rateLimited    prometheus.Counter
}

// newBusinessMetrics allocates and registers the business counters on reg.
// Label series that are known up front are pre-initialized to 0 so they
// appear in /metrics (and on dashboards) before the first occurrence.
func newBusinessMetrics(reg *prometheus.Registry) *businessMetrics {
	shorten := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "links_shortened_total",
		Help: "Successful link creations partitioned by outcome (created vs deduped).",
	}, []string{"outcome"})

	redirect := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "links_redirects_total",
		Help: "Redirect lookups partitioned by how they resolved.",
	}, []string{"outcome"})

	codeCollisions := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "links_code_collisions_total",
		Help: "Auto-generated short-code collisions that triggered a retry.",
	})

	rateLimited := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "links_rate_limited_total",
		Help: "Requests rejected by the per-IP create rate limiter (HTTP 429).",
	})

	reg.MustRegister(shorten, redirect, codeCollisions, rateLimited)

	// Pre-create the known label series so they export as 0.
	for _, o := range []handlers.ShortenOutcome{handlers.ShortenCreated, handlers.ShortenDeduped} {
		shorten.WithLabelValues(string(o))
	}
	for _, o := range []handlers.RedirectOutcome{
		handlers.RedirectCacheHit, handlers.RedirectNegativeHit, handlers.RedirectStoreHit,
		handlers.RedirectNotFound, handlers.RedirectGone, handlers.RedirectError,
	} {
		redirect.WithLabelValues(string(o))
	}

	return &businessMetrics{
		shorten:        shorten,
		redirect:       redirect,
		codeCollisions: codeCollisions,
		rateLimited:    rateLimited,
	}
}

func (m *businessMetrics) IncShorten(o handlers.ShortenOutcome) {
	m.shorten.WithLabelValues(string(o)).Inc()
}

func (m *businessMetrics) IncRedirect(o handlers.RedirectOutcome) {
	m.redirect.WithLabelValues(string(o)).Inc()
}

func (m *businessMetrics) IncCodeCollision() {
	m.codeCollisions.Inc()
}

// incRateLimited records one rate-limiter rejection. Kept as a method (not
// a handlers.Metrics member) because the rejection happens in middleware,
// not the links handler.
func (m *businessMetrics) incRateLimited() {
	m.rateLimited.Inc()
}
