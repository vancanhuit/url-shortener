package handlers_test

import (
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

	"github.com/labstack/echo/v5"

	"github.com/vancanhuit/url-shortener/internal/handlers"
	"github.com/vancanhuit/url-shortener/internal/store"
)

// --- fakes ------------------------------------------------------------------

// fakeStore implements handlers.LinkStore against an in-memory map. It also
// records inserts so the auto-collision retry test can force collisions.
type fakeStore struct {
	mu       sync.Mutex
	links    map[string]store.Link
	clicks   map[string]int64 // separate counter so tests can poll without racing on links[]
	nextID   int64
	failNew  error // non-nil makes the next CreateLink return failNew
	failList error // non-nil makes every ListLinks return failList
}

func newFakeStore() *fakeStore {
	return &fakeStore{links: map[string]store.Link{}, clicks: map[string]int64{}, nextID: 1}
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
	f.clicks[code]++
	if l, ok := f.links[code]; ok {
		l.ClickCount = f.clicks[code]
		f.links[code] = l
	}
	return nil
}

func (f *fakeStore) GetLinkByCode(_ context.Context, _ store.DBTX, code string) (store.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if l, ok := f.links[code]; ok {
		return l, nil
	}
	return store.Link{}, store.ErrNotFound
}

// GetLinkByTargetURL mirrors the production semantics: pick the oldest
// non-expiring row (lowest id, expires_at IS NULL) whose target_url
// matches exactly.
func (f *fakeStore) GetLinkByTargetURL(_ context.Context, _ store.DBTX, targetURL string) (store.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var (
		out   store.Link
		found bool
	)
	for _, l := range f.links {
		if l.TargetURL != targetURL || l.ExpiresAt != nil {
			continue
		}
		if !found || l.ID < out.ID {
			out = l
			found = true
		}
	}
	if !found {
		return store.Link{}, store.ErrNotFound
	}
	return out, nil
}

// ListLinks returns the stored links sorted by id descending, mirroring
// the production ordering. beforeID, when > 0, filters to ids < beforeID.
// When failList is set, every call returns that error -- used to verify
// graceful degradation when the database is unavailable.
func (f *fakeStore) ListLinks(_ context.Context, _ store.DBTX, limit int, beforeID int64) ([]store.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failList != nil {
		return nil, f.failList
	}
	all := make([]store.Link, 0, len(f.links))
	for _, l := range f.links {
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

func newHandlerWithCache(t *testing.T, st handlers.LinkStore, cc handlers.LinkCache, gen handlers.Generator) (*echo.Echo, *handlers.Links) {
	t.Helper()
	h := handlers.NewLinks(handlers.LinksConfig{
		Store:     st,
		Cache:     cc,
		Generator: gen,
		BaseURL:   baseURL,
	})
	e := echo.New()
	h.Mount(e)
	return e, h
}

func doJSON(t *testing.T, e *echo.Echo, method, path, body string) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	out, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return rec, out
}

func decodeLink(t *testing.T, body []byte) handlers.LinkResponse {
	t.Helper()
	var resp handlers.LinkResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal LinkResponse: %v (body=%s)", err, string(body))
	}
	return resp
}

func decodeError(t *testing.T, body []byte) handlers.ErrorResponse {
	t.Helper()
	var resp handlers.ErrorResponse
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
	if got.Code != handlers.ErrCodeCodeTaken {
		t.Errorf("error code = %q, want %q", got.Code, handlers.ErrCodeCodeTaken)
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
	e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{codes: []string{"normcode"}})

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
		v := v
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
		wantCode   string
	}{
		{"invalid_json", `{not json`, http.StatusBadRequest, handlers.ErrCodeInvalidJSONBody},
		{"missing_target_url", `{}`, http.StatusUnprocessableEntity, handlers.ErrCodeValidation},
		{"empty_target_url", `{"target_url":""}`, http.StatusUnprocessableEntity, handlers.ErrCodeValidation},
		{"non_http_scheme", `{"target_url":"ftp://x.example"}`, http.StatusUnprocessableEntity, handlers.ErrCodeValidation},
		{"no_host", `{"target_url":"http://"}`, http.StatusUnprocessableEntity, handlers.ErrCodeValidation},
		{"too_long", `{"target_url":"https://x.example/` + strings.Repeat("a", 2100) + `"}`, http.StatusUnprocessableEntity, handlers.ErrCodeValidation},
		{"bad_user_code", `{"target_url":"https://x.example","code":"!!!"}`, http.StatusUnprocessableEntity, handlers.ErrCodeValidation},
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
	if got := decodeError(t, body); got.Code != handlers.ErrCodeNotFound {
		t.Errorf("error code = %q, want %q", got.Code, handlers.ErrCodeNotFound)
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
	if got := decodeError(t, body); got.Code != handlers.ErrCodeLinkExpired {
		t.Errorf("error code = %q, want %q", got.Code, handlers.ErrCodeLinkExpired)
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

	for i := 0; i < 3; i++ {
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
