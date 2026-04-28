package buildinfo

import "testing"

func TestGet_DefaultsArePopulated(t *testing.T) {
	t.Parallel()

	info := Get()
	if info.Version == "" {
		t.Error("Version should never be empty")
	}
	if info.Commit == "" {
		t.Error("Commit should never be empty")
	}
	if info.Date == "" {
		t.Error("Date should never be empty")
	}
}
