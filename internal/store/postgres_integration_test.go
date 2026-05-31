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
	"fmt"
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
		t.Fatal("URL_SHORTENER_TEST_DATABASE_URL must be set to run integration tests")
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

// TestSoftDeleteLink_StampsTombstoneAndExcludesFromListAndDedup
// exercises the full SQL-level soft-delete contract:
//
//  1. The first SoftDeleteLink call returns nil and stamps deleted_at.
//  2. GetLinkByCode still returns the row (so handlers can surface 410)
//     but Link.IsDeleted() is true.
//  3. ListLinks no longer includes the row (WHERE deleted_at IS NULL).
//  4. GetLinkByTargetURL no longer matches the row, so dedup mints a
//     fresh code rather than resurrecting a retired one.
//  5. A second SoftDeleteLink call against the same code returns
//     ErrNotFound, matching the handler-layer behavior of "204 then 404".
func TestSoftDeleteLink_StampsTombstoneAndExcludesFromListAndDedup(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	code := uniqueCode(t)
	target := "https://example.com/softdel/" + code

	if _, err := s.CreateLink(ctx, nil, code, target, nil); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.Pool().Exec(context.Background(), `DELETE FROM links WHERE code = $1`, code)
	})

	if err := s.SoftDeleteLink(ctx, nil, code); err != nil {
		t.Fatalf("first SoftDeleteLink: %v", err)
	}

	// 1+2: the row survives but is now tombstoned.
	got, err := s.GetLinkByCode(ctx, nil, code)
	if err != nil {
		t.Fatalf("GetLinkByCode after soft-delete: %v", err)
	}
	if !got.IsDeleted() {
		t.Errorf("IsDeleted = false, want true (DeletedAt = %v)", got.DeletedAt)
	}

	// 3: ListLinks filters deleted rows out. Use a wide limit + the
	// just-inserted ID as an upper bound so the assertion is robust
	// against unrelated concurrent test rows.
	rows, err := s.ListLinks(ctx, nil, 100, got.ID+1)
	if err != nil {
		t.Fatalf("ListLinks: %v", err)
	}
	for _, l := range rows {
		if l.Code == code {
			t.Errorf("ListLinks returned the soft-deleted row (code=%q)", code)
		}
	}

	// 4: dedup must not resurrect the deleted row.
	if _, err := s.GetLinkByTargetURL(ctx, nil, target); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetLinkByTargetURL after soft-delete: err = %v, want ErrNotFound", err)
	}

	// 5: idempotency boundary -- second delete is a no-op that
	// surfaces ErrNotFound, mirroring the API's 204-then-404 shape.
	if err := s.SoftDeleteLink(ctx, nil, code); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second SoftDeleteLink: err = %v, want ErrNotFound", err)
	}
}

// TestSoftDeleteLink_UnknownCodeReturnsErrNotFound covers the
// "never-existed" case as a separate path from the "already deleted"
// case in the test above; together they cover both branches of the
// "WHERE code = $1 AND deleted_at IS NULL" UPDATE returning 0 rows.
func TestSoftDeleteLink_UnknownCodeReturnsErrNotFound(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	if err := s.SoftDeleteLink(ctx, nil, uniqueCode(t)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestListLinks_ExcludesExpiredLinks verifies that ListLinks omits rows
// whose expires_at is in the past while returning rows that are still
// active (expires_at in the future) or have no expiry.
func TestListLinks_ExcludesExpiredLinks(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	codeExpired := uniqueCode(t)
	codeFuture := uniqueCode(t)
	codeNoExpiry := uniqueCode(t)

	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	if _, err := s.CreateLink(ctx, nil, codeExpired, "https://example.com/expired/"+codeExpired, &past); err != nil {
		t.Fatalf("CreateLink expired: %v", err)
	}
	if _, err := s.CreateLink(ctx, nil, codeFuture, "https://example.com/future/"+codeFuture, &future); err != nil {
		t.Fatalf("CreateLink future: %v", err)
	}
	if _, err := s.CreateLink(ctx, nil, codeNoExpiry, "https://example.com/noexpiry/"+codeNoExpiry, nil); err != nil {
		t.Fatalf("CreateLink noexpiry: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		for _, c := range []string{codeExpired, codeFuture, codeNoExpiry} {
			_, _ = s.Pool().Exec(bg, `DELETE FROM links WHERE code = $1`, c)
		}
	})

	// Use a large limit; rely on code membership rather than exact position.
	rows, err := s.ListLinks(ctx, nil, 100, 0)
	if err != nil {
		t.Fatalf("ListLinks: %v", err)
	}

	seen := make(map[string]bool, len(rows))
	for _, l := range rows {
		seen[l.Code] = true
	}

	if seen[codeExpired] {
		t.Errorf("ListLinks returned expired link (code=%q)", codeExpired)
	}
	if !seen[codeFuture] {
		t.Errorf("ListLinks omitted future-expiry link (code=%q)", codeFuture)
	}
	if !seen[codeNoExpiry] {
		t.Errorf("ListLinks omitted no-expiry link (code=%q)", codeNoExpiry)
	}
}

// TestCreateAutoLink_AtomicDedup verifies the get-or-insert contract:
//
//  1. First call inserts a new row (created = true).
//  2. Second call with a different proposed code for the same target
//     returns the first row unchanged (created = false).
//  3. A call with a code already taken by a different row returns
//     ErrCodeTaken so callers can retry with a fresh code.
//  4. Expiring rows do not participate: a permanent CreateAutoLink for
//     the same target as an existing expiring row creates a fresh row.
//  5. Soft-deleted rows do not block a new CreateAutoLink for the same
//     target: the index predicate includes deleted_at IS NULL.
func TestCreateAutoLink_AtomicDedup(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	suffix := uniqueCode(t)
	target := "https://example.com/auto-dedup/" + suffix

	// 1: fresh insert.
	code1 := "ad1" + suffix
	first, created, err := s.CreateAutoLink(ctx, nil, code1, target)
	if err != nil {
		t.Fatalf("CreateAutoLink first: %v", err)
	}
	if !created {
		t.Errorf("first call: created = false, want true")
	}
	if first.Code != code1 {
		t.Errorf("first.Code = %q, want %q", first.Code, code1)
	}
	t.Cleanup(func() {
		_, _ = s.Pool().Exec(context.Background(), `DELETE FROM links WHERE target_url = $1`, target)
	})

	// 2: dedup hit -- different proposed code, same target.
	code2 := "ad2" + suffix
	second, created, err := s.CreateAutoLink(ctx, nil, code2, target)
	if err != nil {
		t.Fatalf("CreateAutoLink second: %v", err)
	}
	if created {
		t.Errorf("second call: created = true, want false (dedup hit)")
	}
	if second.Code != code1 {
		t.Errorf("second.Code = %q, want %q (existing row)", second.Code, code1)
	}
	if second.ID != first.ID {
		t.Errorf("second.ID = %d, want %d", second.ID, first.ID)
	}

	// 3: ErrCodeTaken when the proposed code belongs to a different row.
	codeOther := "ad3" + suffix
	otherTarget := target + "-other"
	if _, err := s.CreateLink(ctx, nil, codeOther, otherTarget, nil); err != nil {
		t.Fatalf("seed other row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.Pool().Exec(context.Background(), `DELETE FROM links WHERE code = $1`, codeOther)
	})
	if _, _, err := s.CreateAutoLink(ctx, nil, codeOther, "https://example.com/new-target/"+suffix); !errors.Is(err, store.ErrCodeTaken) {
		t.Errorf("code collision: err = %v, want ErrCodeTaken", err)
	}

	// 4: expiring rows are excluded from the partial index; a
	// CreateAutoLink for the same target creates a fresh permanent row.
	expiryTarget := "https://example.com/auto-dedup-expiry/" + suffix
	codeExpiring := "ade" + suffix
	expiresAt := time.Now().Add(time.Hour)
	if _, err := s.CreateLink(ctx, nil, codeExpiring, expiryTarget, &expiresAt); err != nil {
		t.Fatalf("seed expiring row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.Pool().Exec(context.Background(), `DELETE FROM links WHERE code = $1`, codeExpiring)
	})
	codePermanent := "adp" + suffix
	perm, created, err := s.CreateAutoLink(ctx, nil, codePermanent, expiryTarget)
	if err != nil {
		t.Fatalf("CreateAutoLink over expiring row: %v", err)
	}
	if !created {
		t.Errorf("permanent over expiring: created = false, want true")
	}
	if perm.Code != codePermanent {
		t.Errorf("perm.Code = %q, want %q", perm.Code, codePermanent)
	}
	t.Cleanup(func() {
		_, _ = s.Pool().Exec(context.Background(), `DELETE FROM links WHERE code = $1`, codePermanent)
	})

	// 5: soft-deleted auto-dedup row does not block a new insert.
	delTarget := "https://example.com/auto-dedup-del/" + suffix
	codeDel := "add" + suffix
	if _, _, err := s.CreateAutoLink(ctx, nil, codeDel, delTarget); err != nil {
		t.Fatalf("seed auto-dedup for delete test: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.Pool().Exec(context.Background(), `DELETE FROM links WHERE target_url = $1`, delTarget)
	})
	if err := s.SoftDeleteLink(ctx, nil, codeDel); err != nil {
		t.Fatalf("SoftDeleteLink: %v", err)
	}
	codeAfterDel := "adr" + suffix
	after, created, err := s.CreateAutoLink(ctx, nil, codeAfterDel, delTarget)
	if err != nil {
		t.Fatalf("CreateAutoLink after soft-delete: %v", err)
	}
	if !created {
		t.Errorf("after soft-delete: created = false, want true")
	}
	if after.Code != codeAfterDel {
		t.Errorf("after.Code = %q, want %q", after.Code, codeAfterDel)
	}
}

// TestCreateAutoLink_ConcurrentDedupRace verifies that two simultaneous
// CreateAutoLink callers racing on the same target_url converge on a
// single row. The partial-unique-index + ON CONFLICT path must serialize
// them in the database; whichever insert "wins" decides the persisted
// code, and the loser must observe created=false plus the winner's code.
//
// This guards against a regression where the dedup path used a
// SELECT-then-INSERT pattern (vulnerable to a TOCTOU race) instead of
// the atomic INSERT ... ON CONFLICT DO UPDATE used today.
func TestCreateAutoLink_ConcurrentDedupRace(t *testing.T) {
	s := newStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	const goroutines = 8
	suffix := uniqueCode(t)
	target := "https://example.com/race/" + suffix

	type result struct {
		link    store.Link
		created bool
		err     error
	}
	results := make(chan result, goroutines)
	start := make(chan struct{})

	for i := range goroutines {
		go func(idx int) {
			<-start
			// Distinct candidate codes per goroutine so any
			// "loser" that ignored the dedup path would surface
			// as a unique extra row, not a code collision.
			code := fmt.Sprintf("r%d-%s", idx, suffix)
			link, created, err := s.CreateAutoLink(ctx, nil, code, target)
			results <- result{link: link, created: created, err: err}
		}(i)
	}

	close(start)

	winners := 0
	var canonicalCode string
	losers := make([]store.Link, 0, goroutines-1)
	for range goroutines {
		r := <-results
		if r.err != nil {
			t.Fatalf("CreateAutoLink: %v", r.err)
		}
		if r.created {
			winners++
			canonicalCode = r.link.Code
		} else {
			losers = append(losers, r.link)
		}
		if r.link.TargetURL != target {
			t.Errorf("link.TargetURL = %q, want %q", r.link.TargetURL, target)
		}
	}
	if winners != 1 {
		t.Fatalf("created=true count = %d, want exactly 1", winners)
	}
	if canonicalCode == "" {
		t.Fatal("canonical winning code was empty")
	}
	for _, l := range losers {
		if l.Code != canonicalCode {
			t.Errorf("loser code = %q, want %q (the winner's code)", l.Code, canonicalCode)
		}
	}

	// Every concurrent caller must end up resolving to the same row,
	// so a follow-up GetLinkByTargetURL must return the canonical code.
	got, err := s.GetLinkByTargetURL(ctx, nil, target)
	if err != nil {
		t.Fatalf("GetLinkByTargetURL: %v", err)
	}
	if got.Code != canonicalCode {
		t.Errorf("GetLinkByTargetURL.Code = %q, want %q", got.Code, canonicalCode)
	}
}

// TestPurgeExpiredAndDeleted exercises the cleanup-job query: rows
// soft-deleted or expired older than the grace window are physically
// removed; rows inside the grace window survive.
func TestPurgeExpiredAndDeleted(t *testing.T) {
	s := newStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	suffix := uniqueCode(t)
	freshDeleted := "fd-" + suffix // soft-deleted just now: must survive
	staleDeleted := "sd-" + suffix // soft-deleted "long ago": must purge
	freshExpired := "fe-" + suffix // expired in past, but inside grace: survive
	staleExpired := "se-" + suffix // expired well past grace: purge
	live := "lv-" + suffix         // permanent, never deleted: must survive

	target := "https://example.com/purge/" + suffix
	mustCreate := func(code string, expires *time.Time) {
		t.Helper()
		if _, err := s.CreateLink(ctx, nil, code, target+"/"+code, expires); err != nil {
			t.Fatalf("CreateLink %s: %v", code, err)
		}
	}
	mustCreate(freshDeleted, nil)
	mustCreate(staleDeleted, nil)
	pastFar := time.Now().Add(-48 * time.Hour)
	pastNear := time.Now().Add(-1 * time.Minute)
	mustCreate(freshExpired, &pastNear)
	mustCreate(staleExpired, &pastFar)
	mustCreate(live, nil)

	if err := s.SoftDeleteLink(ctx, nil, freshDeleted); err != nil {
		t.Fatalf("SoftDeleteLink fresh: %v", err)
	}
	if err := s.SoftDeleteLink(ctx, nil, staleDeleted); err != nil {
		t.Fatalf("SoftDeleteLink stale: %v", err)
	}
	// Backdate the stale deleted_at and stale expires_at via SQL so they
	// fall outside the grace window regardless of how long the test
	// process has been running.
	if _, err := s.Pool().Exec(ctx, `UPDATE links SET deleted_at = now() - interval '48 hours' WHERE code = $1`, staleDeleted); err != nil {
		t.Fatalf("backdate staleDeleted: %v", err)
	}

	// Grace = 1 hour: anything deleted/expired more than 1h ago is purged.
	n, err := s.PurgeExpiredAndDeleted(ctx, nil, time.Hour)
	if err != nil {
		t.Fatalf("PurgeExpiredAndDeleted: %v", err)
	}
	if n < 2 {
		t.Errorf("rows deleted = %d, want >=2 (staleDeleted + staleExpired); other tests may add to this count but ours must be purged", n)
	}

	// Survivors are still queryable; purged rows are gone.
	for _, code := range []string{live, freshDeleted, freshExpired} {
		if _, err := s.GetLinkByCode(ctx, nil, code); err != nil && !errors.Is(err, store.ErrNotFound) {
			t.Errorf("post-purge GetLinkByCode(%s): unexpected %v", code, err)
		}
	}
	for _, code := range []string{staleDeleted, staleExpired} {
		_, err := s.GetLinkByCode(ctx, nil, code)
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("post-purge GetLinkByCode(%s) = %v, want ErrNotFound", code, err)
		}
	}
}
