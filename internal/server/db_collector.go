package server

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// poolStatsCollector is a Prometheus collector that surfaces pgxpool
// statistics on each scrape. It reads stats lazily via statFn so the
// values are always current at collection time rather than cached.
//
// The pool stats are split between gauges (point-in-time pool occupancy)
// and counters (monotonic lifetime totals). Exposing both lets operators
// alert on saturation (acquired ≈ max for sustained periods) and on
// contention (rising canceled/empty acquires).
type poolStatsCollector struct {
	statFn func() *pgxpool.Stat

	acquiredConns      *prometheus.Desc
	constructingConns  *prometheus.Desc
	idleConns          *prometheus.Desc
	totalConns         *prometheus.Desc
	maxConns           *prometheus.Desc
	newConnsCount      *prometheus.Desc
	acquireCount       *prometheus.Desc
	acquireDuration    *prometheus.Desc
	canceledAcquire    *prometheus.Desc
	emptyAcquire       *prometheus.Desc
	maxLifetimeDestroy *prometheus.Desc
	maxIdleDestroy     *prometheus.Desc
}

// newPoolStatsCollector builds a collector backed by statFn. statFn is
// typically store.Pool().Stat.
func newPoolStatsCollector(statFn func() *pgxpool.Stat) *poolStatsCollector {
	const ns = "pgxpool"
	d := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(ns+"_"+name, help, nil, nil)
	}
	return &poolStatsCollector{
		statFn:             statFn,
		acquiredConns:      d("acquired_conns", "Connections currently in use."),
		constructingConns:  d("constructing_conns", "Connections currently being established."),
		idleConns:          d("idle_conns", "Idle connections in the pool."),
		totalConns:         d("total_conns", "Total connections in the pool (idle + in use + constructing)."),
		maxConns:           d("max_conns", "Maximum size of the pool."),
		newConnsCount:      d("new_conns_total", "Total connections established by the pool."),
		acquireCount:       d("acquire_total", "Total successful connection acquisitions."),
		acquireDuration:    d("acquire_duration_seconds_total", "Cumulative time spent acquiring connections."),
		canceledAcquire:    d("canceled_acquire_total", "Acquisitions canceled by context."),
		emptyAcquire:       d("empty_acquire_total", "Acquisitions that had to wait for a connection."),
		maxLifetimeDestroy: d("max_lifetime_destroy_total", "Connections destroyed for exceeding MaxConnLifetime."),
		maxIdleDestroy:     d("max_idle_destroy_total", "Connections destroyed for exceeding MaxConnIdleTime."),
	}
}

func (c *poolStatsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.acquiredConns
	ch <- c.constructingConns
	ch <- c.idleConns
	ch <- c.totalConns
	ch <- c.maxConns
	ch <- c.newConnsCount
	ch <- c.acquireCount
	ch <- c.acquireDuration
	ch <- c.canceledAcquire
	ch <- c.emptyAcquire
	ch <- c.maxLifetimeDestroy
	ch <- c.maxIdleDestroy
}

func (c *poolStatsCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.statFn()
	if s == nil {
		return
	}
	g := func(desc *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, v)
	}
	ct := func(desc *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.CounterValue, v)
	}
	g(c.acquiredConns, float64(s.AcquiredConns()))
	g(c.constructingConns, float64(s.ConstructingConns()))
	g(c.idleConns, float64(s.IdleConns()))
	g(c.totalConns, float64(s.TotalConns()))
	g(c.maxConns, float64(s.MaxConns()))
	ct(c.newConnsCount, float64(s.NewConnsCount()))
	ct(c.acquireCount, float64(s.AcquireCount()))
	ct(c.acquireDuration, s.AcquireDuration().Seconds())
	ct(c.canceledAcquire, float64(s.CanceledAcquireCount()))
	ct(c.emptyAcquire, float64(s.EmptyAcquireCount()))
	ct(c.maxLifetimeDestroy, float64(s.MaxLifetimeDestroyCount()))
	ct(c.maxIdleDestroy, float64(s.MaxIdleDestroyCount()))
}
