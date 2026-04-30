package handlers_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/vancanhuit/url-shortener/internal/handlers"
	"github.com/vancanhuit/url-shortener/web"
)

// newWebSetup spins up a Web handler backed by the in-memory link fakes
// and a parsed template set from web/templates.
func newWebSetup(t *testing.T) (*echo.Echo, *fakeStore) {
	t.Helper()
	st, cc := newFakeStore(), newFakeCache()
	links := handlers.NewLinks(handlers.LinksConfig{
		Store:     st,
		Cache:     cc,
		Generator: &scriptedGen{codes: []string{"webcode", "webcod2", "webcod3"}},
		BaseURL:   baseURL,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	tmpl, err := web.ParseTemplates()
	if err != nil {
		t.Fatalf("ParseTemplates: %v", err)
	}
	w := handlers.NewWeb(handlers.WebConfig{
		Links:     links,
		Templates: tmpl,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	e := echo.New()
	w.Mount(e, web.Static())
	return e, st
}

// seedLinks inserts n links into st in deterministic order; the most
// recent link gets the highest id (which matches Postgres BIGSERIAL).
func seedLinks(t *testing.T, st *fakeStore, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		code := fmt.Sprintf("seed%03d", i)
		target := fmt.Sprintf("https://example.com/%d", i)
		if _, err := st.CreateLink(context.Background(), nil, code, target); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func TestWeb_IndexRendersFormAndEmptyRecent(t *testing.T) {
	t.Parallel()
	e, _ := newWebSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`hx-post="/links"`,
		`name="target_url"`,
		"No links yet.",
		`<script src="/static/htmx.min.js"`,
		`href="/static/styles.css"`,
		`<title>URL Shortener</title>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index missing %q", want)
		}
	}
}

func TestWeb_IndexShowsFirstPageAndLoadMoreButton(t *testing.T) {
	t.Parallel()
	e, st := newWebSetup(t)
	// Seed > one page; expect a Load more button.
	seedLinks(t, st, 12)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	body := rec.Body.String()
	// First page = newest 10 (seed011 down through seed002).
	for i := 11; i >= 2; i-- {
		want := fmt.Sprintf("https://example.com/%d", i)
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// Older entries shouldn't be on the first page.
	if strings.Contains(body, "https://example.com/1</span>") {
		t.Errorf("page should not include older entry /1")
	}
	if !strings.Contains(body, `id="load-more"`) {
		t.Errorf("expected Load more button in body")
	}
}

func TestWeb_IndexHidesLoadMoreOnSinglePage(t *testing.T) {
	t.Parallel()
	e, st := newWebSetup(t)
	seedLinks(t, st, 3)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, `id="load-more"`) {
		t.Errorf("did not expect Load more button: %q", body)
	}
}

func TestWeb_LoadMoreReturnsNextPageWithOOBPagination(t *testing.T) {
	t.Parallel()
	e, st := newWebSetup(t)
	seedLinks(t, st, 25)

	// First page returns the cursor for the second page; reproduce that
	// computation here by asking the index.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	cursor := extractLoadMoreCursor(t, rec.Body.String())
	if cursor <= 0 {
		t.Fatalf("no cursor on first page (body=%s)", rec.Body.String())
	}

	// Now fetch the next page via the HTMX endpoint.
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/recent?before=%d", cursor), nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	// Second page = ids 15..6 -> example.com/14 down to example.com/5.
	// Use </span> as the right boundary because </ is not a substring
	// of any URL number, avoiding the /4 vs /14 ambiguity.
	if !strings.Contains(body, "https://example.com/14</span>") {
		t.Errorf("page 2 missing /14: %q", body)
	}
	if !strings.Contains(body, "https://example.com/5</span>") {
		t.Errorf("page 2 missing /5: %q", body)
	}
	// /4 belongs to page 3, so it must NOT appear in this page.
	if strings.Contains(body, "https://example.com/4</span>") {
		t.Errorf("page 2 unexpectedly contains /4")
	}
	if got := strings.Count(body, "<li "); got != 10 {
		t.Errorf("expected 10 <li> rows, got %d", got)
	}
	if !strings.Contains(body, `hx-swap-oob="true"`) {
		t.Errorf("page 2 missing OOB swap div")
	}
	if !strings.Contains(body, `id="load-more"`) {
		t.Errorf("page 2 missing new Load more button")
	}
}

func TestWeb_LoadMoreLastPageOmitsButton(t *testing.T) {
	t.Parallel()
	e, st := newWebSetup(t)
	seedLinks(t, st, 12)

	// Cursor pointing past the most-recent rows -> only 2 rows remain.
	req := httptest.NewRequest(http.MethodGet, "/recent?before=3", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, `id="load-more"`) {
		t.Errorf("last page should not have Load more button: %q", body)
	}
}

func TestWeb_PostLinksRendersSuccessAndIncludesOOBRecentList(t *testing.T) {
	t.Parallel()
	e, _ := newWebSetup(t)

	form := strings.NewReader("target_url=https://example.com/abc")
	req := httptest.NewRequest(http.MethodPost, "/links", form)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	wantShortURL := baseURL + "/r/webcode"
	if !strings.Contains(body, wantShortURL) {
		t.Errorf("body missing short_url %q", wantShortURL)
	}
	// Out-of-band swap section refreshes the recent list with the new link.
	if !strings.Contains(body, `id="recent" hx-swap-oob="true"`) {
		t.Errorf("body missing OOB swap div")
	}
	if !strings.Contains(body, "https://example.com/abc") {
		t.Errorf("OOB recent list missing the just-created link")
	}
	// And no cookies are set anymore (DB-backed list).
	if len(rec.Result().Cookies()) != 0 {
		t.Errorf("did not expect any cookies, got %+v", rec.Result().Cookies())
	}
}

func TestWeb_PostLinksValidationErrorRendersErrorPartial(t *testing.T) {
	t.Parallel()
	e, _ := newWebSetup(t)

	form := strings.NewReader("target_url=ftp://nope.example")
	req := httptest.NewRequest(http.MethodPost, "/links", form)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "must use http or https") {
		t.Errorf("expected validation error in body")
	}
}

func TestWeb_IndexDegradesGracefullyWhenStoreFails(t *testing.T) {
	t.Parallel()
	e, st := newWebSetup(t)
	// Pre-existing data shouldn't matter -- the failure must not leak.
	seedLinks(t, st, 3)
	st.failList = errors.New("simulated database outage")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	// Form must still render so the user has something to interact with.
	if !strings.Contains(body, `hx-post="/links"`) {
		t.Errorf("form missing from degraded response")
	}
	// And the empty-state placeholder, since we have no items.
	if !strings.Contains(body, "No links yet.") {
		t.Errorf("empty-state placeholder missing from degraded response")
	}
}

func TestWeb_StaticAssetsServeWithCacheHeader(t *testing.T) {
	t.Parallel()
	e, _ := newWebSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/static/styles.css", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "max-age=") {
		t.Errorf("Cache-Control = %q, want max-age=...", got)
	}
}

func TestWeb_StaticAssetsServed(t *testing.T) {
	t.Parallel()
	e, _ := newWebSetup(t)

	for _, p := range []string{"/static/styles.css", "/static/htmx.min.js", "/static/copy.js"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", p, rec.Code)
		}
	}
}

// extractLoadMoreCursor pulls the `before=NNN` value out of a Load more
// button's hx-get attribute. Returns 0 when the button isn't present.
func extractLoadMoreCursor(t *testing.T, body string) int64 {
	t.Helper()
	const marker = `hx-get="/recent?before=`
	i := strings.Index(body, marker)
	if i < 0 {
		return 0
	}
	rest := body[i+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return 0
	}
	var n int64
	if _, err := fmt.Sscanf(rest[:end], "%d", &n); err != nil {
		t.Fatalf("parse cursor %q: %v", rest[:end], err)
	}
	return n
}
