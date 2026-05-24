package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreate_WritesExpectedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path, err := Create(dir, "add_users")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	base := filepath.Base(path)
	if base != "00001_add_users.sql" {
		t.Fatalf("filename = %q, want 00001_add_users.sql", base)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "-- +goose Up") {
		t.Error("file missing '-- +goose Up' directive")
	}
	if !strings.Contains(content, "-- +goose Down") {
		t.Error("file missing '-- +goose Down' directive")
	}
}

func TestCreate_SequentialVersioning(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Seed two existing migration files.
	for _, name := range []string{"00001_create_links.sql", "00003_add_clicks.sql"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(""), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	path, err := Create(dir, "next_step")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if filepath.Base(path) != "00004_next_step.sql" {
		t.Fatalf("filename = %q, want 00004_next_step.sql", filepath.Base(path))
	}
}

func TestCreate_EmptyNameReturnsError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := Create(dir, "")
	if err == nil {
		t.Fatal("Create() with empty name: want error, got nil")
	}
}

func TestCreate_NonExistentDirReturnsError(t *testing.T) {
	t.Parallel()

	_, err := Create(filepath.Join(t.TempDir(), "nonexistent"), "foo")
	if err == nil {
		t.Fatal("Create() with missing dir: want error, got nil")
	}
}

func TestNextVersion_EmptyDirReturnsOne(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	v, err := nextVersion(dir)
	if err != nil {
		t.Fatalf("nextVersion() error = %v", err)
	}
	if v != 1 {
		t.Fatalf("nextVersion() = %d, want 1", v)
	}
}
