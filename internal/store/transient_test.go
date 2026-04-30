package store_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vancanhuit/url-shortener/internal/store"
)

// TestIsTransient walks every SQLSTATE class we care about so the
// classifier can't silently widen or narrow without flipping a test.
// Uses external_test (store_test) intentionally: IsTransient is part
// of the package's public surface and behaviour.
func TestIsTransient(t *testing.T) {
	t.Parallel()

	pgErr := func(code string) error {
		return &pgconn.PgError{Code: code, Message: "synthetic " + code}
	}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"non-pg error", errors.New("boom"), false},
		{"unique violation", pgErr("23505"), false},
		{"check violation", pgErr("23514"), false},
		{"connection exception", pgErr("08000"), false}, // pgxpool handles, see doc comment
		{"connection failure", pgErr("08006"), false},
		{"integrity violation in tx-rollback class", pgErr("40002"), false},
		{"serialization failure", pgErr("40001"), true},
		{"statement completion unknown", pgErr("40003"), true},
		{"deadlock detected", pgErr("40P01"), true},
		// Wrapped transient stays transient.
		{"wrapped deadlock", fmt.Errorf("calling x: %w", pgErr("40P01")), true},
		// Wrapped non-transient stays non-transient.
		{"wrapped unique", fmt.Errorf("calling x: %w", pgErr("23505")), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := store.IsTransient(tc.err); got != tc.want {
				t.Errorf("IsTransient(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}
