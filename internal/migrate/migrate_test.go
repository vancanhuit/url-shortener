package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLatestEmbeddedVersion(t *testing.T) {
	t.Parallel()

	got, err := latestEmbeddedVersion()
	if err != nil {
		t.Fatalf("latestEmbeddedVersion() error = %v", err)
	}
	if got != 4 {
		t.Fatalf("latestEmbeddedVersion() = %d, want 4", got)
	}
}

func TestCreate_SQLFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := Create(dir, "add-test-index", "sql"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}
	if len(entries) != 1 {
		t.Fatalf("created files = %d, want 1", len(entries))
	}

	name := entries[0].Name()
	if !strings.HasSuffix(name, "_add_test_index.sql") {
		t.Fatalf("filename = %q, want suffix _add_test_index.sql", name)
	}
	fullPath := filepath.Join(dir, name)
	if _, err := os.Stat(fullPath); err != nil {
		t.Fatalf("created file missing at %q: %v", fullPath, err)
	}
}

func TestCreate_EmptyName(t *testing.T) {
	t.Parallel()

	err := Create(t.TempDir(), "", "sql")
	if err == nil {
		t.Fatal("Create() error = nil, want non-nil")
	}
}
