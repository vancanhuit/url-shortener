package store

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Test the merge logic in isolation. This is an internal_test (same
// package) because applyPoolConfig is unexported -- the function is a
// pure transformation on *pgxpool.Config and the alternative
// (exporting it) would widen the API for no consumer benefit.
func TestApplyPoolConfig(t *testing.T) {
	t.Parallel()

	// A real pgxpool.ParseConfig is the only way to mint a *Config
	// with the pgx defaults populated; constructing one with `&Config{}`
	// would zero out fields we explicitly want to preserve.
	baseURL := "postgres://user:pass@localhost:5432/db?sslmode=disable"

	t.Run("zero PoolConfig leaves pgx defaults intact", func(t *testing.T) {
		t.Parallel()
		cfg, err := pgxpool.ParseConfig(baseURL)
		if err != nil {
			t.Fatalf("ParseConfig: %v", err)
		}
		before := *cfg

		applyPoolConfig(cfg, PoolConfig{})

		if cfg.MaxConns != before.MaxConns {
			t.Errorf("MaxConns mutated: got %d, want %d", cfg.MaxConns, before.MaxConns)
		}
		if cfg.MinConns != before.MinConns {
			t.Errorf("MinConns mutated: got %d, want %d", cfg.MinConns, before.MinConns)
		}
		if cfg.MaxConnLifetime != before.MaxConnLifetime {
			t.Errorf("MaxConnLifetime mutated: got %v, want %v", cfg.MaxConnLifetime, before.MaxConnLifetime)
		}
		if cfg.MaxConnIdleTime != before.MaxConnIdleTime {
			t.Errorf("MaxConnIdleTime mutated: got %v, want %v", cfg.MaxConnIdleTime, before.MaxConnIdleTime)
		}
		if cfg.HealthCheckPeriod != before.HealthCheckPeriod {
			t.Errorf("HealthCheckPeriod mutated: got %v, want %v", cfg.HealthCheckPeriod, before.HealthCheckPeriod)
		}
	})

	t.Run("non-zero fields override defaults", func(t *testing.T) {
		t.Parallel()
		cfg, err := pgxpool.ParseConfig(baseURL)
		if err != nil {
			t.Fatalf("ParseConfig: %v", err)
		}

		pc := PoolConfig{
			MaxConns:          25,
			MinConns:          5,
			MaxConnLifetime:   30 * time.Minute,
			MaxConnIdleTime:   5 * time.Minute,
			HealthCheckPeriod: 15 * time.Second,
		}
		applyPoolConfig(cfg, pc)

		if cfg.MaxConns != pc.MaxConns {
			t.Errorf("MaxConns = %d, want %d", cfg.MaxConns, pc.MaxConns)
		}
		if cfg.MinConns != pc.MinConns {
			t.Errorf("MinConns = %d, want %d", cfg.MinConns, pc.MinConns)
		}
		if cfg.MaxConnLifetime != pc.MaxConnLifetime {
			t.Errorf("MaxConnLifetime = %v, want %v", cfg.MaxConnLifetime, pc.MaxConnLifetime)
		}
		if cfg.MaxConnIdleTime != pc.MaxConnIdleTime {
			t.Errorf("MaxConnIdleTime = %v, want %v", cfg.MaxConnIdleTime, pc.MaxConnIdleTime)
		}
		if cfg.HealthCheckPeriod != pc.HealthCheckPeriod {
			t.Errorf("HealthCheckPeriod = %v, want %v", cfg.HealthCheckPeriod, pc.HealthCheckPeriod)
		}
	})

	t.Run("partial override leaves untouched fields alone", func(t *testing.T) {
		t.Parallel()
		cfg, err := pgxpool.ParseConfig(baseURL)
		if err != nil {
			t.Fatalf("ParseConfig: %v", err)
		}
		origLifetime := cfg.MaxConnLifetime
		origIdle := cfg.MaxConnIdleTime

		applyPoolConfig(cfg, PoolConfig{MaxConns: 50})

		if cfg.MaxConns != 50 {
			t.Errorf("MaxConns = %d, want 50", cfg.MaxConns)
		}
		if cfg.MaxConnLifetime != origLifetime {
			t.Errorf("MaxConnLifetime should be untouched, got %v want %v", cfg.MaxConnLifetime, origLifetime)
		}
		if cfg.MaxConnIdleTime != origIdle {
			t.Errorf("MaxConnIdleTime should be untouched, got %v want %v", cfg.MaxConnIdleTime, origIdle)
		}
	})
}
