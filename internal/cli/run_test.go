package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/vancanhuit/url-shortener/internal/config"
)

func TestEnsureSchemaCurrent_AutoMigrateRunsUp(t *testing.T) {
	t.Parallel()

	origUp := migrateUpFn
	origVersions := migrateVersionsFn
	t.Cleanup(func() {
		migrateUpFn = origUp
		migrateVersionsFn = origVersions
	})

	upCalled := false
	versionsCalled := false
	migrateUpFn = func(_ context.Context, _ string) error {
		upCalled = true
		return nil
	}
	migrateVersionsFn = func(_ context.Context, _ string) (int64, int64, error) {
		versionsCalled = true
		return 0, 0, nil
	}

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cfg := config.Config{AutoMigrate: true, DatabaseURL: "postgres://example"}

	if err := ensureSchemaCurrent(cmd, cfg); err != nil {
		t.Fatalf("ensureSchemaCurrent() error = %v", err)
	}
	if !upCalled {
		t.Fatal("migrate.Up was not called")
	}
	if versionsCalled {
		t.Fatal("migrate.Versions should not be called when auto_migrate=true")
	}
}

func TestEnsureSchemaCurrent_BehindSchemaReturnsActionableError(t *testing.T) {
	t.Parallel()

	origUp := migrateUpFn
	origVersions := migrateVersionsFn
	t.Cleanup(func() {
		migrateUpFn = origUp
		migrateVersionsFn = origVersions
	})

	migrateUpFn = func(_ context.Context, _ string) error {
		t.Fatal("migrate.Up should not be called when auto_migrate=false")
		return nil
	}
	migrateVersionsFn = func(_ context.Context, _ string) (int64, int64, error) {
		return 3, 4, nil
	}

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cfg := config.Config{AutoMigrate: false, DatabaseURL: "postgres://example"}

	err := ensureSchemaCurrent(cmd, cfg)
	if err == nil {
		t.Fatal("ensureSchemaCurrent() error = nil, want non-nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "database schema is behind embedded migrations") {
		t.Fatalf("error = %q, want behind-schema message", msg)
	}
	if !strings.Contains(msg, "current=3 latest=4") {
		t.Fatalf("error = %q, want current/latest values", msg)
	}
}

func TestEnsureSchemaCurrent_PropagatesVersionsError(t *testing.T) {
	t.Parallel()

	origUp := migrateUpFn
	origVersions := migrateVersionsFn
	t.Cleanup(func() {
		migrateUpFn = origUp
		migrateVersionsFn = origVersions
	})

	want := errors.New("boom")
	migrateVersionsFn = func(_ context.Context, _ string) (int64, int64, error) {
		return 0, 0, want
	}

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cfg := config.Config{AutoMigrate: false, DatabaseURL: "postgres://example"}

	err := ensureSchemaCurrent(cmd, cfg)
	if !errors.Is(err, want) {
		t.Fatalf("ensureSchemaCurrent() error = %v, want %v", err, want)
	}
}
