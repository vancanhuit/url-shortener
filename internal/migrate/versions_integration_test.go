//go:build integration

package migrate_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/vancanhuit/url-shortener/internal/migrate"
)

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("URL_SHORTENER_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("URL_SHORTENER_TEST_DATABASE_URL not set; skipping integration test")
	}
	return url
}

// TestVersions_CurrentMatchesLatestAtRuntime exercises the preflight source
// of truth against a real Postgres: the DB migration version that goose sees
// should match the latest embedded migration after an `Up`.
func TestVersions_CurrentMatchesLatestAtRuntime(t *testing.T) {
	url := testDatabaseURL(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	if err := migrate.Up(ctx, url); err != nil {
		t.Fatalf("migrate.Up: %v", err)
	}

	current, latest, err := migrate.Versions(ctx, url)
	if err != nil {
		t.Fatalf("migrate.Versions: %v", err)
	}
	if current != latest {
		t.Fatalf("migrate.Versions current=%d latest=%d; want equal", current, latest)
	}
}

// TestRedo_AppliesCurrentMigrationAgain verifies that Redo leaves the DB at
// the same version it started at (down then up = net-zero version change).
func TestRedo_AppliesCurrentMigrationAgain(t *testing.T) {
	url := testDatabaseURL(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	if err := migrate.Up(ctx, url); err != nil {
		t.Fatalf("migrate.Up: %v", err)
	}

	before, _, err := migrate.Versions(ctx, url)
	if err != nil {
		t.Fatalf("migrate.Versions (before): %v", err)
	}

	if err := migrate.Redo(ctx, url); err != nil {
		t.Fatalf("migrate.Redo: %v", err)
	}

	after, _, err := migrate.Versions(ctx, url)
	if err != nil {
		t.Fatalf("migrate.Versions (after): %v", err)
	}
	if before != after {
		t.Fatalf("Redo changed version: before=%d after=%d; want equal", before, after)
	}
}
