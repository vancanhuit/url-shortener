package server

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// TestPoolStatsCollector_Describe verifies every metric descriptor is
// emitted, which keeps Describe and Collect in sync (a mismatch would
// trip Prometheus' consistency checks at registration time).
func TestPoolStatsCollector_Describe(t *testing.T) {
	t.Parallel()
	c := newPoolStatsCollector(func() *pgxpool.Stat { return nil })

	ch := make(chan *prometheus.Desc, 32)
	c.Describe(ch)
	close(ch)

	const want = 12
	got := 0
	for range ch {
		got++
	}
	if got != want {
		t.Errorf("Describe emitted %d descriptors, want %d", got, want)
	}
}

// TestPoolStatsCollector_NilStatIsSafe verifies Collect is a no-op when
// statFn yields nil (e.g. a pool that hasn't been initialized), rather
// than panicking on a scrape.
func TestPoolStatsCollector_NilStatIsSafe(t *testing.T) {
	t.Parallel()
	c := newPoolStatsCollector(func() *pgxpool.Stat { return nil })

	ch := make(chan prometheus.Metric, 32)
	c.Collect(ch)
	close(ch)

	if n := len(ch); n != 0 {
		t.Errorf("Collect emitted %d metrics for nil stat, want 0", n)
	}
}

// TestPoolStatsCollector_RegistersCleanly verifies the collector passes
// Prometheus' Describe/Collect consistency validation at registration.
func TestPoolStatsCollector_RegistersCleanly(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	if err := reg.Register(newPoolStatsCollector(func() *pgxpool.Stat { return nil })); err != nil {
		t.Fatalf("Register: %v", err)
	}
}
