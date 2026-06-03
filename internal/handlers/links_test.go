package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"

	openapi "github.com/vancanhuit/url-shortener/api"

	"github.com/vancanhuit/url-shortener/internal/handlers"
	"github.com/vancanhuit/url-shortener/internal/store"
)

// --- fakes ------------------------------------------------------------------

// fakeStore implements handlers.LinkStore against an in-memory map. It also
// records inserts so the auto-collision retry test can force collisions.
type fakeStore struct {
	mu         sync.Mutex
	links      map[string]store.Link
	autoDedup  map[string]string // targetURL -> code for permanent auto-dedup links
	clicks     map[string]int64  // separate counter so tests can poll without racing on links[]
	getByCode  int               // counts GetLinkByCode invocations (used to assert cache short-circuit)
	clickErrs  []error           // queued errors returned by IncrementClicks; popped per call. nil entries pass through to the real bump.
	clickCalls int               // total IncrementClicks invocations (success + failure), for retry-budget assertions.
	nextID     int64
	failNew    error // non-nil makes the next CreateLink return failNew
	failList   error // non-nil makes every ListLinks return failList
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		links:     map[string]store.Link{},
		autoDedup: map[string]string{},
		clicks:    map[string]int64{},
		nextID:    1,
	}
}

// CreateLink mirrors the production signature: expiresAt may be nil for a
// permanent link. The pointer is copied (not shared) so a caller mutating
// its expiresAt after the call doesn't retroactively change stored state.
func (f *fakeStore) CreateLink(_ context.Context, _ store.DBTX, code, target string, expiresAt *time.Time) (store.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNew != nil {
		err := f.failNew
		f.failNew = nil
		return store.Link{}, err
	}
	if _, ok := f.links[code]; ok {
		return store.Link{}, store.ErrCodeTaken
	}
	var exp *time.Time
	if expiresAt != nil {
		t := *expiresAt
		exp = &t
	}
	l := store.Link{ID: f.nextID, Code: code, TargetURL: target, CreatedAt: time.Unix(0, 0).UTC(), ExpiresAt: exp}
	f.nextID++
	f.links[code] = l
	return l, nil
}

// IncrementClicks bumps the in-memory click counter, which the link
// view also surfaces on its next GetLinkByCode read (so tests asserting
// on the exposed ClickCount see the change).
func (f *fakeStore) IncrementClicks(_ context.Context, _ store.DBTX, code string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clickCalls++
	if len(f.clickErrs) > 0 {
		err := f.clickErrs[0]
		f.clickErrs = f.clickErrs[1:]
		if err != nil {
			return err
		}
	}
	f.clicks[code]++
	if l, ok := f.links[code]; ok {
		l.ClickCount = f.clicks[code]
		f.links[code] = l
	}
	return nil
}

// GetLinkByCode returns the stored link regardless of expiry or deletion,
// mirroring the production store which also returns any row (even
// soft-deleted or expired ones). The handler is responsible for calling
// link.IsExpired() / link.IsDeleted() and mapping those to 410 Gone.
// Contrast with ListLinks below, which filters both conditions at the
// store level matching the SQL WHERE clause.
func (f *fakeStore) GetLinkByCode(_ context.Context, _ store.DBTX, code string) (store.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getByCode++
	if l, ok := f.links[code]; ok {
		return l, nil
	}
	return store.Link{}, store.ErrNotFound
}

// CreateAutoLink mirrors the production INSERT ... ON CONFLICT DO UPDATE
// semantics: it returns the existing permanent auto-dedup row for targetURL
// when one exists (created = false), or inserts a new row and registers it
// in the autoDedup map (created = true). ErrCodeTaken is returned when the
// proposed code is already taken by a different row so callers can retry.
func (f *fakeStore) CreateAutoLink(_ context.Context, _ store.DBTX, code, targetURL string) (store.Link, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Dedup: return the existing permanent auto-dedup row if present.
	if existing, ok := f.autoDedup[targetURL]; ok {
		if l, ok := f.links[existing]; ok {
			return l, false, nil
		}
		// Stale entry (link was deleted etc.) -- fall through to insert.
		delete(f.autoDedup, targetURL)
	}
	if f.failNew != nil {
		err := f.failNew
		f.failNew = nil
		return store.Link{}, false, err
	}
	if _, ok := f.links[code]; ok {
		return store.Link{}, false, store.ErrCodeTaken
	}
	l := store.Link{ID: f.nextID, Code: code, TargetURL: targetURL, CreatedAt: time.Unix(0, 0).UTC()}
	f.nextID++
	f.links[code] = l
	f.autoDedup[targetURL] = code
	return l, true, nil
}

// ListLinks returns the stored links sorted by id descending, mirroring
// the production ordering. beforeID, when > 0, filters to ids < beforeID.
// Soft-deleted and expired rows are excluded so the fake matches what the
// SQL implementation does. When failList is set, every call returns that
// error -- used to verify graceful degradation when the database is
// unavailable.
func (f *fakeStore) ListLinks(_ context.Context, _ store.DBTX, limit int, beforeID int64) ([]store.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failList != nil {
		return nil, f.failList
	}
	now := time.Now()
	all := make([]store.Link, 0, len(f.links))
	for _, l := range f.links {
		if l.DeletedAt != nil {
			continue
		}
		if l.ExpiresAt != nil && !l.ExpiresAt.After(now) {
			continue
		}
		if beforeID > 0 && l.ID >= beforeID {
			continue
		}
		all = append(all, l)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID > all[j].ID })
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// SoftDeleteLink mirrors store.Store.SoftDeleteLink: stamps DeletedAt
// on a live row, returns store.ErrNotFound if the code is absent or
// already deleted. Failure injection isn't wired here because the
// existing tests never need it; add a `failDelete error` field if a
// future test does.
func (f *fakeStore) SoftDeleteLink(_ context.Context, _ store.DBTX, code string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.links[code]
	if !ok || l.DeletedAt != nil {
		return store.ErrNotFound
	}
	now := time.Now()
	l.DeletedAt = &now
	f.links[code] = l
	return nil
}

// fakeCache implements handlers.LinkCache against an in-memory map. TTL is
// recorded but not enforced -- the handler tests don't exercise expiry.
type fakeCache struct {
	mu     sync.Mutex
	values map[string]string
	ttls   map[string]time.Duration
	gets   int
	sets   int
}

func newFakeCache() *fakeCache {
	return &fakeCache{values: map[string]string{}, ttls: map[string]time.Duration{}}
}

func (f *fakeCache) Get(_ context.Context, key string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	v, ok := f.values[key]
	return v, ok, nil
}

func (f *fakeCache) Set(_ context.Context, key, value string, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets++
	f.values[key] = value
	f.ttls[key] = ttl
	return nil
}

func (f *fakeCache) Del(_ context.Context, keys ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range keys {
		delete(f.values, k)
		delete(f.ttls, k)
	}
	return nil
}

// scriptedGen returns a fixed sequence of codes; useful for forcing the
// collision retry path deterministically.
type scriptedGen struct {
	codes []string
	idx   int
}

func (s *scriptedGen) Generate() (string, error) {
	if s.idx >= len(s.codes) {
		return "", errors.New("scriptedGen: exhausted")
	}
	c := s.codes[s.idx]
	s.idx++
	return c, nil
}

// --- helpers ----------------------------------------------------------------

const baseURL = "https://short.test"

func newHandlerWithCache(t *testing.T, st handlers.LinkStore, cc handlers.LinkCache, gen handlers.Generator) (chi.Router, *handlers.Links) {
	t.Helper()
	return newHandlerWithTTLs(t, st, cc, gen, 0, 0)
}

func newHandlerWithTTLs(
	t *testing.T,
	st handlers.LinkStore,
	cc handlers.LinkCache,
	gen handlers.Generator,
	cacheTTL, negCacheTTL time.Duration,
) (chi.Router, *handlers.Links) {
	t.Helper()
	h := handlers.NewLinks(handlers.LinksConfig{
		Store:            st,
		Cache:            cc,
		Generator:        gen,
		BaseURL:          baseURL,
		CacheTTL:         cacheTTL,
		NegativeCacheTTL: negCacheTTL,
	})
	r := chi.NewRouter()
	h.Mount(r)
	return r, h
}

func doJSON(t *testing.T, e chi.Router, method, path, body string) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	out, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return rec, out
}

func decodeLink(t *testing.T, body []byte) openapi.LinkResponse {
	t.Helper()
	var resp openapi.LinkResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal LinkResponse: %v (body=%s)", err, string(body))
	}
	return resp
}

func decodeError(t *testing.T, body []byte) openapi.ErrorResponse {
	t.Helper()
	var resp openapi.ErrorResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal ErrorResponse: %v (body=%s)", err, string(body))
	}
	return resp
}

// --- Create -----------------------------------------------------------------

func TestCreate_AutoGeneratedCodeReturns201WithShortURL(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{codes: []string{"abc1234"}})

	rec, body := doJSON(t, e, http.MethodPost, "/api/v1/links",
		`{"target_url":"https://example.com/long/path"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, string(body))
	}
	resp := decodeLink(t, body)
	if resp.Code != "abc1234" {
		t.Errorf("Code = %q, want abc1234", resp.Code)
	}
	if resp.ShortURL != baseURL+"/r/abc1234" {
		t.Errorf("ShortURL = %q, want %q", resp.ShortURL, baseURL+"/r/abc1234")
	}
	if resp.TargetURL != "https://example.com/long/path" {
		t.Errorf("TargetURL = %q", resp.TargetURL)
	}
	// Cache populated on create so subsequent redirects don't roundtrip the DB.
	if cc.sets != 1 {
		t.Errorf("cache.sets = %d, want 1", cc.sets)
	}
}

func TestCreate_UserSuppliedCodeIsAcceptedAndPersisted(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{codes: []string{"unused"}})

	rec, body := doJSON(t, e, http.MethodPost, "/api/v1/links",
		`{"target_url":"https://example.com","code":"my-code-1"}`)
	// "my-code-1" contains a hyphen, which ValidCode rejects -> 422.
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s", rec.Code, string(body))
	}

	rec, body = doJSON(t, e, http.MethodPost, "/api/v1/links",
		`{"target_url":"https://example.com","code":"mycode1"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, string(body))
	}
	if got := decodeLink(t, body).Code; got != "mycode1" {
		t.Errorf("Code = %q, want mycode1", got)
	}
}

func TestCreate_DuplicateUserSuppliedCodeReturns409(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	_, _ = doJSON(t, e, http.MethodPost, "/api/v1/links",
		`{"target_url":"https://example.com","code":"taken1"}`)
	rec, body := doJSON(t, e, http.MethodPost, "/api/v1/links",
		`{"target_url":"https://other.example","code":"taken1"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, string(body))
	}
	got := decodeError(t, body)
	if got.Error == "" {
		t.Errorf("expected non-empty error message")
	}
	if got.Code != openapi.ErrorResponseCodeCodeTaken {
		t.Errorf("error code = %q, want %q", got.Code, openapi.ErrorResponseCodeCodeTaken)
	}
}

func TestCreate_RetriesPastAutoGeneratedCollisions(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	// pre-seed the first two codes to force two collisions; third succeeds.
	if _, err := st.CreateLink(context.Background(), nil, "first00", "https://x", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := st.CreateLink(context.Background(), nil, "second0", "https://y", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	gen := &scriptedGen{codes: []string{"first00", "second0", "third00"}}
	e, _ := newHandlerWithCache(t, st, cc, gen)

	rec, body := doJSON(t, e, http.MethodPost, "/api/v1/links",
		`{"target_url":"https://example.com"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, string(body))
	}
	if got := decodeLink(t, body).Code; got != "third00" {
		t.Errorf("Code = %q, want third00", got)
	}
	if gen.idx != 3 {
		t.Errorf("generator calls = %d, want 3", gen.idx)
	}
}

func TestCreate_DedupsAutoGeneratedSameTargetReturns200WithExistingCode(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{codes: []string{"firstcode", "shouldnotuse"}})

	// First POST creates a fresh row -> 201.
	rec, body := doJSON(t, e, http.MethodPost, "/api/v1/links",
		`{"target_url":"https://example.com/dedupe"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first status = %d, body = %s", rec.Code, string(body))
	}
	first := decodeLink(t, body)

	// Second POST with the same target reuses the existing code -> 200.
	rec, body = doJSON(t, e, http.MethodPost, "/api/v1/links",
		`{"target_url":"https://example.com/dedupe"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200; body = %s", rec.Code, string(body))
	}
	second := decodeLink(t, body)
	if second.Code != first.Code {
		t.Errorf("second code = %q, want same as first %q", second.Code, first.Code)
	}
}

func TestCreate_NormalizesScheme_Host_AndDefaultPort(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{codes: []string{
		"normcode",
		// The 3 variant POSTs each generate a candidate code before
		// CreateAutoLink returns the existing row (dedup hit). Provide
		// dummy codes so scriptedGen does not exhaust.
		"dup1", "dup2", "dup3",
	}})

	// Seed with a canonical form.
	rec, body := doJSON(t, e, http.MethodPost, "/api/v1/links",
		`{"target_url":"http://example.com/foo"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed status = %d, body = %s", rec.Code, string(body))
	}
	canonical := decodeLink(t, body)

	// The canonical row's stored target_url must already be the
	// normalised form, regardless of how it was originally posted.
	// Without this the dedup lookup would silently miss on subsequent
	// variants that *do* get normalised.
	if canonical.TargetURL != "http://example.com/foo" {
		t.Errorf("canonical target_url = %q, want canonical normalised form", canonical.TargetURL)
	}

	// Each variation should normalize to the canonical form and dedup.
	variants := []string{
		"HTTP://Example.COM/foo",
		"http://EXAMPLE.com:80/foo",
		"http://example.com/foo",
	}
	for _, v := range variants {
		rec, body := doJSON(t, e, http.MethodPost, "/api/v1/links",
			`{"target_url":"`+v+`"}`)
		if rec.Code != http.StatusOK {
			t.Errorf("variant %q: status = %d, want 200", v, rec.Code)
			continue
		}
		got := decodeLink(t, body)
		if got.Code != canonical.Code {
			t.Errorf("variant %q: code = %q, want %q", v, got.Code, canonical.Code)
		}
		if got.TargetURL != canonical.TargetURL {
			t.Errorf("variant %q: target_url = %q, want canonical %q",
				v, got.TargetURL, canonical.TargetURL)
		}
	}
}

func TestCreate_UserSuppliedCodeAlwaysCreates_DoesNotDedupOnTarget(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{codes: []string{"autocode"}})

	// Auto-generated row for the target.
	rec, _ := doJSON(t, e, http.MethodPost, "/api/v1/links",
		`{"target_url":"https://example.com/dual"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("auto status = %d", rec.Code)
	}
	// User-supplied code for the same target must create a new row.
	rec, body := doJSON(t, e, http.MethodPost, "/api/v1/links",
		`{"target_url":"https://example.com/dual","code":"mycode2"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("user status = %d, want 201; body = %s", rec.Code, string(body))
	}
	if got := decodeLink(t, body).Code; got != "mycode2" {
		t.Errorf("user code = %q, want mycode2", got)
	}
}

func TestCreate_BadInputReturns4xx(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, body string
		wantStatus int
		wantCode   openapi.ErrorResponseCode
	}{
		{"invalid_json", `{not json`, http.StatusBadRequest, openapi.ErrorResponseCodeInvalidJSONBody},
		{"missing_target_url", `{}`, http.StatusUnprocessableEntity, openapi.ErrorResponseCodeValidationFailed},
		{"empty_target_url", `{"target_url":""}`, http.StatusUnprocessableEntity, openapi.ErrorResponseCodeValidationFailed},
		{"non_http_scheme", `{"target_url":"ftp://x.example"}`, http.StatusUnprocessableEntity, openapi.ErrorResponseCodeValidationFailed},
		{"no_host", `{"target_url":"http://"}`, http.StatusUnprocessableEntity, openapi.ErrorResponseCodeValidationFailed},
		{"too_long", `{"target_url":"https://x.example/` + strings.Repeat("a", 2100) + `"}`, http.StatusUnprocessableEntity, openapi.ErrorResponseCodeValidationFailed},
		{"bad_user_code", `{"target_url":"https://x.example","code":"!!!"}`, http.StatusUnprocessableEntity, openapi.ErrorResponseCodeValidationFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			st, cc := newFakeStore(), newFakeCache()
			e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{codes: []string{"unused0"}})
			rec, body := doJSON(t, e, http.MethodPost, "/api/v1/links", tt.body)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := decodeError(t, body); got.Code != tt.wantCode {
				t.Errorf("error code = %q, want %q (body=%s)", got.Code, tt.wantCode, body)
			}
		})
	}
}

// --- Get --------------------------------------------------------------------

func TestGet_ReturnsLinkJSON(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	if _, err := st.CreateLink(context.Background(), nil, "fixed01", "https://example.com", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	rec, body := doJSON(t, e, http.MethodGet, "/api/v1/links/fixed01", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, string(body))
	}
	resp := decodeLink(t, body)
	if resp.Code != "fixed01" {
		t.Errorf("Code = %q", resp.Code)
	}
	if resp.ShortURL != baseURL+"/r/fixed01" {
		t.Errorf("ShortURL = %q", resp.ShortURL)
	}
}

func TestGet_UnknownCodeReturns404(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})
	rec, body := doJSON(t, e, http.MethodGet, "/api/v1/links/missing", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if got := decodeError(t, body); got.Code != openapi.ErrorResponseCodeNotFound {
		t.Errorf("error code = %q, want %q", got.Code, openapi.ErrorResponseCodeNotFound)
	}
}

func TestGet_InvalidCodeReturns404(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})
	rec, _ := doJSON(t, e, http.MethodGet, "/api/v1/links/aa", "") // < MinLength
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// --- Redirect ---------------------------------------------------------------

func TestRedirect_HitsCacheWithoutTouchingStore(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	if err := cc.Set(context.Background(), "link:cached1", "https://cached.example", time.Minute); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	req := httptest.NewRequest(http.MethodGet, "/r/cached1", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://cached.example" {
		t.Errorf("Location = %q", loc)
	}
	if cc.gets != 1 {
		t.Errorf("cache.gets = %d, want 1", cc.gets)
	}
}

func TestRedirect_FallsBackToStoreAndBackfillsCache(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	if _, err := st.CreateLink(context.Background(), nil, "fromdb1", "https://db.example", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	req := httptest.NewRequest(http.MethodGet, "/r/fromdb1", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://db.example" {
		t.Errorf("Location = %q", loc)
	}
	if got := cc.values["link:fromdb1"]; got != "https://db.example" {
		t.Errorf("cache backfill missing: %q", got)
	}
}

func TestRedirect_UnknownCodeReturns404(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})
	req := httptest.NewRequest(http.MethodGet, "/r/missing", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestRedirect_InvalidCodeReturns404(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})
	req := httptest.NewRequest(http.MethodGet, "/r/aa", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// --- Negative caching -------------------------------------------------------
//
// The redirect path caches "known dead-end" answers under the same
// link:<code> key with a sentinel value, so a scanning attack that
// repeatedly probes the same unknown / deleted / expired codes is
// absorbed by Redis and never reaches Postgres past the first miss.

// TestRedirect_NegativeCacheHitShortCircuits: a pre-seeded sentinel
// value must produce a 404 without any GetLinkByCode call against the
// store. This is the hot path that protects the DB under attack.
func TestRedirect_NegativeCacheHitShortCircuits(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	if err := cc.Set(context.Background(), "link:deadend", "", time.Minute); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	req := httptest.NewRequest(http.MethodGet, "/r/deadend", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if st.getByCode != 0 {
		t.Errorf("store.GetLinkByCode calls = %d, want 0 (cache must short-circuit)", st.getByCode)
	}
}

// TestRedirect_UnknownCodePopulatesNegativeCache: a store miss must
// write the sentinel under the requested code with NegativeCacheTTL,
// so a follow-up request resolves entirely from cache.
func TestRedirect_UnknownCodePopulatesNegativeCache(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	req := httptest.NewRequest(http.MethodGet, "/r/missing", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("first status = %d, want 404", rec.Code)
	}
	v, ok := cc.values["link:missing"]
	if !ok {
		t.Fatalf("negative cache entry missing")
	}
	if v != "" {
		t.Errorf("negative entry = %q, want empty sentinel", v)
	}
	if got := cc.ttls["link:missing"]; got != handlers.NegativeCacheTTL {
		t.Errorf("negative TTL = %v, want %v", got, handlers.NegativeCacheTTL)
	}

	before := st.getByCode
	req2 := httptest.NewRequest(http.MethodGet, "/r/missing", nil)
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Errorf("second status = %d, want 404", rec2.Code)
	}
	if st.getByCode != before {
		t.Errorf("second request hit the store: getByCode %d -> %d", before, st.getByCode)
	}
}

// TestRedirect_ConfiguredNegativeCacheTTLIsUsed: when LinksConfig.NegativeCacheTTL
// is set, that value (not the package-level default) is used as the TTL
// for negative cache entries written on a store miss.
func TestRedirect_ConfiguredNegativeCacheTTLIsUsed(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	customTTL := 5 * time.Minute
	e, _ := newHandlerWithTTLs(t, st, cc, &scriptedGen{}, 0, customTTL)

	req := httptest.NewRequest(http.MethodGet, "/r/missing2", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := cc.ttls["link:missing2"]; got != customTTL {
		t.Errorf("negative cache TTL = %v, want %v", got, customTTL)
	}
}

// TestRedirect_DeletedCodePopulatesNegativeCache: a soft-deleted row
// is permanent dead-end (the unique code constraint prevents reuse),
// so the negative entry is also written. Public response stays 410.
func TestRedirect_DeletedCodePopulatesNegativeCache(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	if _, err := st.CreateLink(context.Background(), nil, "deleted", "https://gone.example", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := st.SoftDeleteLink(context.Background(), nil, "deleted"); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	req := httptest.NewRequest(http.MethodGet, "/r/deleted", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rec.Code)
	}
	if v, ok := cc.values["link:deleted"]; !ok || v != "" {
		t.Errorf("negative entry not written: ok=%v v=%q", ok, v)
	}
}

// TestRedirect_ExpiredCodePopulatesNegativeCache: an expired link is
// also permanent dead-end (we don't un-expire), so the sentinel is
// written. Public response stays 410.
func TestRedirect_ExpiredCodePopulatesNegativeCache(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	past := time.Now().Add(-time.Hour)
	if _, err := st.CreateLink(context.Background(), nil, "expired", "https://old.example", &past); err != nil {
		t.Fatalf("seed: %v", err)
	}
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	req := httptest.NewRequest(http.MethodGet, "/r/expired", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rec.Code)
	}
	if v, ok := cc.values["link:expired"]; !ok || v != "" {
		t.Errorf("negative entry not written: ok=%v v=%q", ok, v)
	}
}

// --- Expiry + clicks --------------------------------------------------------

// TestCreate_AcceptsExpiresAtAndPersistsIt: a JSON request that supplies
// expires_at must be persisted with that exact value, surfaced in the
// response, and skip dedup against an existing permanent row for the
// same target (a permanent dedup hit would silently extend the link's
// lifetime past the requested deadline).
func TestCreate_AcceptsExpiresAtAndPersistsIt(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	// Pre-existing *permanent* row for the same target -- without the
	// dedup skip, the response below would echo "old00" instead of
	// minting a fresh expiring code.
	if _, err := st.CreateLink(context.Background(), nil, "old0001", "https://dup.example", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{codes: []string{"newexp1"}})

	expiresAt := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	body := fmt.Sprintf(`{"target_url":"https://dup.example","expires_at":%q}`, expiresAt.Format(time.RFC3339))
	rec, raw := doJSON(t, e, http.MethodPost, "/api/v1/links", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, string(raw))
	}
	resp := decodeLink(t, raw)
	if resp.Code != "newexp1" {
		t.Errorf("Code = %q, want newexp1 (dedup must skip when expiry is requested)", resp.Code)
	}
	if resp.ExpiresAt == nil || !resp.ExpiresAt.Equal(expiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", resp.ExpiresAt, expiresAt)
	}
}

// TestCreate_RejectsPastExpiresAt: an expires_at in the (well-past)
// past must surface as 422, not silently treat the link as already
// expired or DB-reject it as an error.
func TestCreate_RejectsPastExpiresAt(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{codes: []string{"unused1"}})
	// 1h in the past is well outside the small clock-skew grace window.
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`{"target_url":"https://x.example","expires_at":%q}`, past)
	rec, _ := doJSON(t, e, http.MethodPost, "/api/v1/links", body)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

// TestRedirect_ExpiredLinkReturns410: a redirect on a link past its
// expiry must surface 410 Gone, distinct from the 404 we use for
// unknown codes.
func TestRedirect_ExpiredLinkReturns410(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	past := time.Now().Add(-time.Hour)
	if _, err := st.CreateLink(context.Background(), nil, "expired", "https://gone.example", &past); err != nil {
		t.Fatalf("seed: %v", err)
	}
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	req := httptest.NewRequest(http.MethodGet, "/r/expired", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusGone {
		t.Errorf("status = %d, want 410", rec.Code)
	}
}

// TestRedirect_ErrorsUseErrorResponseSchema verifies that 4xx/5xx
// responses from the redirect handler use the same
// {"error":"...","code":"..."} JSON shape as every other handler,
// rather than a generic error envelope.
func TestRedirect_ErrorsUseErrorResponseSchema(t *testing.T) {
	t.Parallel()

	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	cases := []struct {
		name      string
		path      string
		seed      func(*fakeStore)
		seedCache func(*fakeCache)
		wantCode  int
		wantECode openapi.ErrorResponseCode
	}{
		{
			name:      "unknown code returns not_found",
			path:      "/r/missing1",
			wantCode:  http.StatusNotFound,
			wantECode: openapi.ErrorResponseCodeNotFound,
		},
		{
			name: "deleted link returns link_deleted",
			path: "/r/deldlink",
			seed: func(st *fakeStore) {
				_, _ = st.CreateLink(context.Background(), nil, "deldlink", "https://del.example", nil)
				_ = st.SoftDeleteLink(context.Background(), nil, "deldlink")
			},
			wantCode:  http.StatusGone,
			wantECode: openapi.ErrorResponseCodeLinkDeleted,
		},
		{
			name: "expired link returns link_expired",
			path: "/r/exprlnk",
			seed: func(st *fakeStore) {
				_, _ = st.CreateLink(context.Background(), nil, "exprlnk", "https://exp.example", &past)
			},
			wantCode:  http.StatusGone,
			wantECode: openapi.ErrorResponseCodeLinkExpired,
		},
		{
			name: "negative cache hit returns not_found",
			path: "/r/ncached",
			seed: func(st *fakeStore) {
				// Prime a future-expiry link so it exists in the store,
				// then seed the negative-cache sentinel to short-circuit.
				_, _ = st.CreateLink(context.Background(), nil, "ncached", "https://nc.example", &future)
			},
			seedCache: func(cc *fakeCache) {
				_ = cc.Set(context.Background(), "link:ncached", "", time.Minute)
			},
			wantCode:  http.StatusNotFound,
			wantECode: openapi.ErrorResponseCodeNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st, cc := newFakeStore(), newFakeCache()
			if tc.seed != nil {
				tc.seed(st)
			}
			if tc.seedCache != nil {
				tc.seedCache(cc)
			}
			e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			got := decodeError(t, rec.Body.Bytes())
			if got.Code != tc.wantECode {
				t.Errorf("error code = %q, want %q", got.Code, tc.wantECode)
			}
			if got.Error == "" {
				t.Errorf("error message is empty")
			}
		})
	}
}

// TestGet_ExpiredLinkReturns410: same shape as the redirect, exposed
// via the JSON API so a programmatic client can distinguish a
// once-valid code from an unknown one.
func TestGet_ExpiredLinkReturns410(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	past := time.Now().Add(-time.Hour)
	if _, err := st.CreateLink(context.Background(), nil, "expired", "https://gone.example", &past); err != nil {
		t.Fatalf("seed: %v", err)
	}
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	rec, body := doJSON(t, e, http.MethodGet, "/api/v1/links/expired", "")
	if rec.Code != http.StatusGone {
		t.Errorf("status = %d, want 410", rec.Code)
	}
	if got := decodeError(t, body); got.Code != openapi.ErrorResponseCodeLinkExpired {
		t.Errorf("error code = %q, want %q", got.Code, openapi.ErrorResponseCodeLinkExpired)
	}
}

// TestRedirect_IncrementsClickCount: a successful redirect must
// fire-and-forget bump the link's click counter. We poll the handler's
// background-task WaitGroup so the test stays deterministic without
// sleeping.
func TestRedirect_IncrementsClickCount(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	if _, err := st.CreateLink(context.Background(), nil, "tracked", "https://t.example", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	e, h := newHandlerWithCache(t, st, cc, &scriptedGen{})

	for i := range 3 {
		req := httptest.NewRequest(http.MethodGet, "/r/tracked", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound {
			t.Fatalf("redirect %d: status = %d", i, rec.Code)
		}
	}
	if !h.WaitForBackgroundTasks(2 * time.Second) {
		t.Fatal("click increment goroutines did not complete in time")
	}
	st.mu.Lock()
	got := st.clicks["tracked"]
	st.mu.Unlock()
	if got != 3 {
		t.Errorf("clicks = %d, want 3", got)
	}
}

// TestRedirect_RetriesTransientClickFailure: a deadlock_detected on
// the first IncrementClicks attempt must be retried (with backoff) and
// eventually succeed; the click counter ends up incremented exactly
// once and a non-trivial number of attempts is observed.
func TestRedirect_RetriesTransientClickFailure(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	if _, err := st.CreateLink(context.Background(), nil, "retried", "https://r.example", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Two transient failures, then nil (success). The click counter
	// should still end at 1, and IncrementClicks should have been
	// invoked exactly three times.
	st.clickErrs = []error{
		&pgconn.PgError{Code: "40P01", Message: "deadlock_detected"},
		&pgconn.PgError{Code: "40001", Message: "serialization_failure"},
		nil,
	}
	e, h := newHandlerWithCache(t, st, cc, &scriptedGen{})

	req := httptest.NewRequest(http.MethodGet, "/r/retried", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("redirect status = %d", rec.Code)
	}
	if !h.WaitForBackgroundTasks(2 * time.Second) {
		t.Fatal("click goroutine did not complete in time (retry budget exceeded?)")
	}

	st.mu.Lock()
	calls, count := st.clickCalls, st.clicks["retried"]
	st.mu.Unlock()
	if calls != 3 {
		t.Errorf("IncrementClicks calls = %d, want 3 (initial + two retries)", calls)
	}
	if count != 1 {
		t.Errorf("click count = %d, want 1", count)
	}
}

// TestRedirect_DoesNotRetryNonTransientClickFailure: a non-pg error
// (or one with a non-retryable SQLSTATE) must NOT be retried. The
// counter stays at zero and exactly one attempt is recorded.
func TestRedirect_DoesNotRetryNonTransientClickFailure(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	if _, err := st.CreateLink(context.Background(), nil, "noretry", "https://n.example", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	st.clickErrs = []error{errors.New("some other error")}
	e, h := newHandlerWithCache(t, st, cc, &scriptedGen{})

	req := httptest.NewRequest(http.MethodGet, "/r/noretry", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("redirect status = %d", rec.Code)
	}
	if !h.WaitForBackgroundTasks(2 * time.Second) {
		t.Fatal("click goroutine did not complete in time")
	}

	st.mu.Lock()
	calls, count := st.clickCalls, st.clicks["noretry"]
	st.mu.Unlock()
	if calls != 1 {
		t.Errorf("IncrementClicks calls = %d, want 1 (no retry on non-transient)", calls)
	}
	if count != 0 {
		t.Errorf("click count = %d, want 0 (failed increment must not bump counter)", count)
	}
}

// TestCachePut_ClampsTTLToExpiry: a link whose remaining lifetime is
// shorter than the default cache TTL must be cached for that shorter
// window so the cache cannot serve a redirect for an already-expired
// row. A link whose lifetime is already up (or under a second) must
// not be cached at all.
func TestCachePut_ClampsTTLToExpiry(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	// Short expiry: 5 minutes. Default CacheTTL is 1h.
	soon := time.Now().Add(5 * time.Minute)
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{codes: []string{"shortex"}})
	body := fmt.Sprintf(`{"target_url":"https://short.example","expires_at":%q}`,
		soon.UTC().Format(time.RFC3339Nano))
	rec, _ := doJSON(t, e, http.MethodPost, "/api/v1/links", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	cc.mu.Lock()
	ttl := cc.ttls["link:shortex"]
	cc.mu.Unlock()
	if ttl <= 0 || ttl > 5*time.Minute+time.Second {
		t.Errorf("cache TTL = %v, want <=5m (clamped to remaining lifetime)", ttl)
	}
}

// --- Delete (soft-delete) ---------------------------------------------------

// TestDelete_LiveCodeReturns204AndStampsDeletedAt: the happy path.
// First DELETE flips the row to soft-deleted and returns 204 with no
// body. We assert via the fake's internal state rather than going
// through the API again because subsequent assertions (Get-after-delete
// returns 410, Redirect-after-delete returns 410) get their own tests
// below -- one assertion per test keeps failure messages pointed at
// the actually-broken layer.
func TestDelete_LiveCodeReturns204AndStampsDeletedAt(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	if _, err := st.CreateLink(context.Background(), nil, "live123", "https://example.com", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	rec, body := doJSON(t, e, http.MethodDelete, "/api/v1/links/live123", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, string(body))
	}
	if len(body) != 0 {
		t.Errorf("expected empty body on 204, got %q", string(body))
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	got := st.links["live123"]
	if got.DeletedAt == nil {
		t.Fatal("DeletedAt is nil; expected soft-delete to have stamped it")
	}
	if got.TargetURL != "https://example.com" {
		t.Errorf("TargetURL changed: %q", got.TargetURL)
	}
}

// TestDelete_InvalidatesCacheEntry: a redirect that previously cached
// the target must not keep serving it after the row is soft-deleted.
// Delete primes a negative cache entry, so the prior positive value is
// overwritten with the sentinel rather than merely removed. We seed the
// cache directly (rather than walking through Redirect) to keep the
// assertion narrowly scoped to the cache-invalidation behavior of Delete
// itself.
func TestDelete_InvalidatesCacheEntry(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	if _, err := st.CreateLink(context.Background(), nil, "cachehit", "https://example.com", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Pre-populate the cache to simulate a prior /r/cachehit.
	if err := cc.Set(context.Background(), "link:cachehit", "https://example.com", time.Hour); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	rec, _ := doJSON(t, e, http.MethodDelete, "/api/v1/links/cachehit", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}

	cc.mu.Lock()
	defer cc.mu.Unlock()
	v, present := cc.values["link:cachehit"]
	if !present {
		t.Fatal("cache entry missing after DELETE; want negative sentinel primed")
	}
	if v != "" {
		t.Errorf("cache entry = %q after DELETE; want negative sentinel (empty string)", v)
	}
}

// TestDelete_UnknownCodeReturns404: there is no live row with that
// code -- could be a typo, a never-existed code, or one that was
// already deleted. All three collapse to the same not_found shape;
// the API doesn't distinguish "never existed" from "already deleted"
// to avoid leaking lifecycle metadata to clients that have no business
// inspecting it.
func TestDelete_UnknownCodeReturns404(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	rec, body := doJSON(t, e, http.MethodDelete, "/api/v1/links/nosuch1", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, string(body))
	}
	if got := decodeError(t, body); got.Code != openapi.ErrorResponseCodeNotFound {
		t.Errorf("error code = %q, want %q", got.Code, openapi.ErrorResponseCodeNotFound)
	}
}

// TestDelete_SecondDeleteReturns404: documents that DELETE is
// semantically idempotent (the second call does not re-delete or
// resurrect the row) but not response-shape idempotent: 204 then 404.
// Clients that need 204+204 can collapse the two themselves.
func TestDelete_SecondDeleteReturns404(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	if _, err := st.CreateLink(context.Background(), nil, "twice12", "https://example.com", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	rec, _ := doJSON(t, e, http.MethodDelete, "/api/v1/links/twice12", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("first DELETE status = %d, want 204", rec.Code)
	}
	rec, body := doJSON(t, e, http.MethodDelete, "/api/v1/links/twice12", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second DELETE status = %d, body = %s", rec.Code, string(body))
	}
}

// TestDelete_InvalidCodeShapeReturns404: codes that fail
// shortener.ValidCode (length, alphabet, ...) are rejected at the
// edge with the same not_found response a missing code would
// produce. Returning 422 here would leak that the API does
// shape-validation on the path parameter, which doesn't help any
// legitimate client.
func TestDelete_InvalidCodeShapeReturns404(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	rec, body := doJSON(t, e, http.MethodDelete, "/api/v1/links/has-hyphen", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, string(body))
	}
}

// TestGet_DeletedLinkReturns410: parallels TestGet_ExpiredLinkReturns410.
// A soft-deleted link must surface a 410 with the link_deleted error
// code so programmatic clients can distinguish it from both
// "never existed" (404) and "expired" (410 + link_expired).
func TestGet_DeletedLinkReturns410(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	if _, err := st.CreateLink(context.Background(), nil, "deleted", "https://example.com", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := st.SoftDeleteLink(context.Background(), nil, "deleted"); err != nil {
		t.Fatalf("seed delete: %v", err)
	}
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	rec, body := doJSON(t, e, http.MethodGet, "/api/v1/links/deleted", "")
	if rec.Code != http.StatusGone {
		t.Errorf("status = %d, want 410", rec.Code)
	}
	if got := decodeError(t, body); got.Code != openapi.ErrorResponseCodeLinkDeleted {
		t.Errorf("error code = %q, want %q", got.Code, openapi.ErrorResponseCodeLinkDeleted)
	}
}

// TestRedirect_DeletedLinkReturns410: the public redirect surfaces
// the same 410 a /api/v1/links lookup does. Mirrors the
// TestRedirect_ExpiredLinkReturns410 shape.
func TestRedirect_DeletedLinkReturns410(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	if _, err := st.CreateLink(context.Background(), nil, "deleted", "https://gone.example", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := st.SoftDeleteLink(context.Background(), nil, "deleted"); err != nil {
		t.Fatalf("seed delete: %v", err)
	}
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	req := httptest.NewRequest(http.MethodGet, "/r/deleted", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusGone {
		t.Errorf("status = %d, want 410", rec.Code)
	}
}

// --- List -------------------------------------------------------------------

// decodeList decodes a /api/v1/links list body into the public shape.
func decodeList(t *testing.T, body []byte) openapi.ListResponse {
	t.Helper()
	var resp openapi.ListResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode list: %v\nbody = %s", err, string(body))
	}
	return resp
}

func TestList_EmptyStoreReturnsEmptyItemsAndNullCursor(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	rec, body := doJSON(t, e, http.MethodGet, "/api/v1/links", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, string(body))
	}
	resp := decodeList(t, body)
	if len(resp.Items) != 0 {
		t.Errorf("items = %d, want 0", len(resp.Items))
	}
	if resp.NextCursor != nil {
		t.Errorf("next_cursor = %v, want null", *resp.NextCursor)
	}
	// Explicit JSON-shape check: the contract says next_cursor is
	// always present (rendered as null) so clients can branch on a
	// single nullability check.
	if !bytes.Contains(body, []byte(`"next_cursor":null`)) {
		t.Errorf("body should render next_cursor:null, got %s", string(body))
	}
}

func TestList_DefaultsTo10NewestFirstWithCursorWhenMore(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	// Seed 12 links so the default page-size of 10 returns 10 + a cursor.
	for i := range 12 {
		code := fmt.Sprintf("code%03d", i)
		if _, err := st.CreateLink(context.Background(), nil, code, fmt.Sprintf("https://example.com/%d", i), nil); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	rec, body := doJSON(t, e, http.MethodGet, "/api/v1/links", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, string(body))
	}
	resp := decodeList(t, body)
	if len(resp.Items) != 10 {
		t.Fatalf("items = %d, want 10", len(resp.Items))
	}
	if resp.NextCursor == nil || *resp.NextCursor <= 0 {
		t.Fatalf("next_cursor = %v, want positive", resp.NextCursor)
	}
	// Newest-first: code011 was inserted last and should lead.
	if resp.Items[0].Code != "code011" {
		t.Errorf("items[0].Code = %q, want code011", resp.Items[0].Code)
	}
}

func TestList_BeforeCursorWalksOlderRows(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	for i := range 5 {
		code := fmt.Sprintf("walk%03d", i)
		if _, err := st.CreateLink(context.Background(), nil, code, fmt.Sprintf("https://example.com/%d", i), nil); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	rec, body := doJSON(t, e, http.MethodGet, "/api/v1/links?limit=2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, string(body))
	}
	first := decodeList(t, body)
	if len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first page: items=%d cursor=%v body=%s", len(first.Items), first.NextCursor, string(body))
	}

	rec, body = doJSON(t, e, http.MethodGet,
		fmt.Sprintf("/api/v1/links?limit=2&before=%d", *first.NextCursor), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("second page status = %d, body = %s", rec.Code, string(body))
	}
	second := decodeList(t, body)
	if len(second.Items) != 2 {
		t.Fatalf("second page items = %d, want 2", len(second.Items))
	}
	// The first row of page 2 must be older than the last row of page 1.
	if second.Items[0].Code == first.Items[1].Code {
		t.Errorf("page 2 should not repeat the cursor row %q", first.Items[1].Code)
	}
}

func TestList_LimitClampedToMax(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	for i := range 3 {
		code := fmt.Sprintf("clamp%02d", i)
		if _, err := st.CreateLink(context.Background(), nil, code, fmt.Sprintf("https://example.com/%d", i), nil); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	// 1000 is far above the 100 cap; the handler should silently
	// clamp rather than 4xx.
	rec, body := doJSON(t, e, http.MethodGet, "/api/v1/links?limit=1000", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, string(body))
	}
	resp := decodeList(t, body)
	if len(resp.Items) != 3 {
		t.Errorf("items = %d, want 3 (no clamp-induced truncation when fewer rows exist)", len(resp.Items))
	}
}

func TestList_BadLimitFallsBackToDefault(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	for i := range 11 {
		code := fmt.Sprintf("bad%04d", i)
		if _, err := st.CreateLink(context.Background(), nil, code, fmt.Sprintf("https://example.com/%d", i), nil); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	// Non-positive integers pass binding but are clamped to the default.
	for _, raw := range []string{"-5", "0"} {
		rec, body := doJSON(t, e, http.MethodGet, "/api/v1/links?limit="+raw, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("limit=%q status = %d, body = %s", raw, rec.Code, string(body))
		}
		resp := decodeList(t, body)
		if len(resp.Items) != 10 {
			t.Errorf("limit=%q items = %d, want default 10", raw, len(resp.Items))
		}
	}

	// Non-integer string fails binding (oapi-codegen validates before handler).
	rec, body := doJSON(t, e, http.MethodGet, "/api/v1/links?limit=abc", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("limit=\"abc\" status = %d, want 400, body = %s", rec.Code, string(body))
	}
}

func TestList_StoreFailureReturns500(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	st.failList = errors.New("boom")
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	rec, body := doJSON(t, e, http.MethodGet, "/api/v1/links", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", rec.Code, string(body))
	}
	if got := decodeError(t, body).Code; got != openapi.ErrorResponseCodeInternalError {
		t.Errorf("error code = %q, want %q", got, openapi.ErrorResponseCodeInternalError)
	}
}

func TestList_ExcludesSoftDeletedRows(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()
	for _, code := range []string{"keep001", "remove1", "keep002"} {
		if _, err := st.CreateLink(context.Background(), nil, code, "https://example.com/"+code, nil); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := st.SoftDeleteLink(context.Background(), nil, "remove1"); err != nil {
		t.Fatalf("seed delete: %v", err)
	}
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})

	rec, body := doJSON(t, e, http.MethodGet, "/api/v1/links", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, string(body))
	}
	resp := decodeList(t, body)
	for _, item := range resp.Items {
		if item.Code == "remove1" {
			t.Errorf("list returned soft-deleted code %q", item.Code)
		}
	}
}

func TestList_ExcludesExpiredLinks(t *testing.T) {
	t.Parallel()
	st, cc := newFakeStore(), newFakeCache()

	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	seeds := []struct {
		code       string
		expiresAt  *time.Time
		wantInList bool
	}{
		{"exp-past", &past, false},
		{"exp-future", &future, true},
		{"exp-none", nil, true},
	}
	for _, s := range seeds {
		if _, err := st.CreateLink(context.Background(), nil, s.code, "https://example.com/"+s.code, s.expiresAt); err != nil {
			t.Fatalf("seed %q: %v", s.code, err)
		}
	}

	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{})
	rec, body := doJSON(t, e, http.MethodGet, "/api/v1/links", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, string(body))
	}
	resp := decodeList(t, body)

	seen := make(map[string]bool, len(resp.Items))
	for _, item := range resp.Items {
		seen[item.Code] = true
	}

	for _, s := range seeds {
		if s.wantInList && !seen[s.code] {
			t.Errorf("list omitted code %q (should be present)", s.code)
		}
		if !s.wantInList && seen[s.code] {
			t.Errorf("list returned code %q (should be excluded: expired)", s.code)
		}
	}
}
