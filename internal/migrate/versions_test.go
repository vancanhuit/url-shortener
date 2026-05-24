package migrate

import "testing"

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
