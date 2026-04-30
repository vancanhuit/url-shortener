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

	created, err := s.CreateLink(ctx, nil, code, "https://example.com/x", nil)
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

	if _, err := s.CreateLink(ctx, nil, code, "https://example.com/a", nil); err != nil {
		t.Fatalf("first CreateLink: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.Pool().Exec(context.Background(), `DELETE FROM links WHERE code = $1`, code)
	})

	_, err := s.CreateLink(ctx, nil, code, "https://example.com/b", nil)
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

// TestGetLinkByTargetURL_ReturnsOldestMatchOrNotFound exercises the dedup
// lookup: when several rows share the same target_url, the oldest (lowest
// id) row is returned; when none match, ErrNotFound surfaces.
func TestGetLinkByTargetURL_ReturnsOldestMatchOrNotFound(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	suffix := uniqueCode(t)
	target := "https://example.com/get-by-target/" + suffix

	first, err := s.CreateLink(ctx, nil, "p"+suffix, target, nil)
	if err != nil {
		t.Fatalf("CreateLink first: %v", err)
	}
	if _, err := s.CreateLink(ctx, nil, "q"+suffix, target, nil); err != nil {
		t.Fatalf("CreateLink second: %v", err)
	}

	got, err := s.GetLinkByTargetURL(ctx, nil, target)
	if err != nil {
		t.Fatalf("GetLinkByTargetURL: %v", err)
	}
	if got.ID != first.ID {
		t.Errorf("got id %d, want oldest %d", got.ID, first.ID)
	}

	if _, err := s.GetLinkByTargetURL(ctx, nil, "https://nope.example/"+suffix); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing target: err = %v, want ErrNotFound", err)
	}
}

// TestListLinks_OrdersAndPaginates verifies the cursor-based pagination
// shape: rows come back in id-DESC order, beforeID excludes ids >= it, and
// limit caps the result.
func TestListLinks_OrdersAndPaginates(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	// Insert three rows; capture the ids in insertion order so we can drive
	// cursor pagination from a known reference point. Each test run uses
	// fresh, unique codes so we don't conflict with other tests / re-runs.
	suffix := uniqueCode(t)
	codes := []string{"a" + suffix, "b" + suffix, "c" + suffix}
	ids := make([]int64, len(codes))
	for i, code := range codes {
		l, err := s.CreateLink(ctx, nil, code, "https://example.com/list/"+code, nil)
		if err != nil {
			t.Fatalf("CreateLink %q: %v", code, err)
		}
		ids[i] = l.ID
	}

	// First "page": ask for 2 newest. We expect the last two we inserted,
	// in reverse insertion order.
	page1, err := s.ListLinks(ctx, nil, 2, 0)
	if err != nil {
		t.Fatalf("ListLinks page 1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page 1 len = %d, want 2", len(page1))
	}
	if page1[0].ID != ids[2] || page1[1].ID != ids[1] {
		t.Errorf("page 1 ids = [%d %d], want [%d %d]", page1[0].ID, page1[1].ID, ids[2], ids[1])
	}

	// Second "page": cursor right after the last row we returned -> only
	// rows with id < ids[1] remain (i.e. ids[0]).
	page2, err := s.ListLinks(ctx, nil, 2, ids[1])
	if err != nil {
		t.Fatalf("ListLinks page 2: %v", err)
	}
	if len(page2) == 0 || page2[0].ID != ids[0] {
		t.Errorf("page 2 should start at ids[0]=%d; got %+v", ids[0], page2)
	}
}

// TestCreateAndIncrementClicks verifies that IncrementClicks bumps the
// counter atomically (multiple invocations sum) and that the new value
// is observable via GetLinkByCode.
func TestCreateAndIncrementClicks(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	code := uniqueCode(t)

	if _, err := s.CreateLink(ctx, nil, code, "https://example.com/clicks/"+code, nil); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.Pool().Exec(context.Background(), `DELETE FROM links WHERE code = $1`, code)
	})

	for i := 0; i < 3; i++ {
		if err := s.IncrementClicks(ctx, nil, code); err != nil {
			t.Fatalf("IncrementClicks: %v", err)
		}
	}
	got, err := s.GetLinkByCode(ctx, nil, code)
	if err != nil {
		t.Fatalf("GetLinkByCode: %v", err)
	}
	if got.ClickCount != 3 {
		t.Errorf("ClickCount = %d, want 3", got.ClickCount)
	}
}

// TestCreateLinkWithExpiry_RoundTripsAndDedupExcludes verifies that
// CreateLink persists a non-nil expires_at, GetLinkByCode echoes it
// back unchanged, and GetLinkByTargetURL excludes expiring rows from
// the dedup lookup (per the partial-index / WHERE clause).
func TestCreateLinkWithExpiry_RoundTripsAndDedupExcludes(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	code := uniqueCode(t)

	expiresAt := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	target := "https://example.com/expiry/" + code
	if _, err := s.CreateLink(ctx, nil, code, target, &expiresAt); err != nil {
		t.Fatalf("CreateLink with expiry: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.Pool().Exec(context.Background(), `DELETE FROM links WHERE code = $1`, code)
	})

	got, err := s.GetLinkByCode(ctx, nil, code)
	if err != nil {
		t.Fatalf("GetLinkByCode: %v", err)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(expiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, expiresAt)
	}

	// Dedup must NOT find this row (expires_at IS NOT NULL).
	if _, err := s.GetLinkByTargetURL(ctx, nil, target); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetLinkByTargetURL on expiring row: err = %v, want ErrNotFound", err)
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

	if _, err := s.CreateLink(ctx, tx, code, "https://example.com/tx", nil); err != nil {
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
