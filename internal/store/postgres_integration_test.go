//go:build integration

// Integration tests for the Postgres store. Run with:
//
//	just test-integration
//
// (which sets `-tags=integration`). Requires a live Postgres reachable via
// $URL_SHORTENER_TEST_DATABASE_URL with the migrations already applied --
// the canonical setup is `just up-test`, which brings up db+redis and runs
// the migration one-shot.

package store_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vancanhuit/url-shortener/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	url := os.Getenv("URL_SHORTENER_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("URL_SHORTENER_TEST_DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	s, err := store.New(ctx, url)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// uniqueCode returns a short code that's safe to insert without colliding
// with other concurrent test runs.
func uniqueCode(t *testing.T) string {
	t.Helper()
	return "t" + time.Now().UTC().Format("150405.000000")
}

func TestCreateAndGetLink(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	code := uniqueCode(t)

	created, err := s.CreateLink(ctx, nil, code, "https://example.com/x")
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
	if created.Code != code {
		t.Errorf("Code = %q, want %q", created.Code, code)
	}
	if created.ID == 0 {
		t.Error("ID should be assigned by the database")
	}
	if created.CreatedAt.IsZero() {
		t.Error("CreatedAt should be populated by the database default")
	}

	got, err := s.GetLinkByCode(ctx, nil, code)
	if err != nil {
		t.Fatalf("GetLinkByCode: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("got ID = %d, want %d", got.ID, created.ID)
	}
	if got.TargetURL != "https://example.com/x" {
		t.Errorf("TargetURL = %q", got.TargetURL)
	}

	// Cleanup so re-runs don't leave rows behind.
	t.Cleanup(func() {
		_, _ = s.Pool().Exec(context.Background(), `DELETE FROM links WHERE code = $1`, code)
	})
}

func TestCreateLink_DuplicateCodeReturnsErrCodeTaken(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	code := uniqueCode(t)

	if _, err := s.CreateLink(ctx, nil, code, "https://example.com/a"); err != nil {
		t.Fatalf("first CreateLink: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.Pool().Exec(context.Background(), `DELETE FROM links WHERE code = $1`, code)
	})

	_, err := s.CreateLink(ctx, nil, code, "https://example.com/b")
	if !errors.Is(err, store.ErrCodeTaken) {
		t.Errorf("err = %v, want ErrCodeTaken", err)
	}
}

func TestGetLinkByCode_MissingReturnsErrNotFound(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	_, err := s.GetLinkByCode(ctx, nil, "definitely-does-not-exist-"+uniqueCode(t))
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestTransaction verifies that store methods participate in a caller-managed
// transaction when a pgx.Tx is passed as the DBTX argument: rolling back the
// tx must drop the inserted row.
func TestTransaction_RollbackDiscardsInsert(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	code := uniqueCode(t)

	tx, err := s.Pool().BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}

	if _, err := s.CreateLink(ctx, tx, code, "https://example.com/tx"); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("CreateLink in tx: %v", err)
	}

	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	// Outside the tx the row should not exist.
	_, err = s.GetLinkByCode(ctx, nil, code)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("after rollback, err = %v, want ErrNotFound", err)
	}
}
