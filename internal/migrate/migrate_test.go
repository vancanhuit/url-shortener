package migrate

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vancanhuit/url-shortener/migrations"
)

// TestEmbeddedMigrationsFS verifies that the embedded migrations FS is
// readable and contains the expected number of SQL files. This catches
// accidental deletion or embed-directive omissions without needing a DB.
func TestEmbeddedMigrationsFS(t *testing.T) {
	t.Parallel()

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("fs.ReadDir: %v", err)
	}

	var sqlFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			sqlFiles = append(sqlFiles, e.Name())
		}
	}
	const wantCount = 5
	if len(sqlFiles) != wantCount {
		t.Fatalf("embedded SQL migration count = %d, want %d (files: %v)",
			len(sqlFiles), wantCount, sqlFiles)
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
