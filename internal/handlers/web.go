// This file implements the HTML user interface: a single-page form that
// posts to itself via HTMX, swapping in a success or error partial. The
// "recent links" list is paginated server-side directly out of Postgres,
// using cursor-based pagination over `links.id` (the BIGSERIAL primary
// key). No per-browser state.
package handlers

import (
	"bytes"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/vancanhuit/url-shortener/internal/store"
)

// recentPageSize bounds the page size for the public "recent links" list.
// Small enough that a full re-render after a create is cheap; large enough
// that the Load more button is rarely needed for casual use.
const recentPageSize = 10

// WebConfig groups the web handler's constructor arguments. Links carries
// the shared business logic (Persist / Resolve / List); Templates is an
// already-parsed template set with the entry points `layout`,
// `link-result`, `link-error`, `recent-list`, and `recent-page` defined.
type WebConfig struct {
	Links     *Links
	Templates *template.Template
	Logger    *slog.Logger
}

// Web is the HTML handler bundle.
type Web struct {
	links  *Links
	tmpl   *template.Template
	logger *slog.Logger
}

// NewWeb constructs a Web handler. Logger defaults to slog.Default.
func NewWeb(cfg WebConfig) *Web {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Web{
		links:  cfg.Links,
		tmpl:   cfg.Templates,
		logger: logger,
	}
}

// Mount registers the HTML routes on e and serves the embedded static
// assets under `/static/`. staticFS should be the rooted FS returned by
// the web package's Static() helper.
//
// Routes:
//   - GET  /                    -- form + first page of recent links
//   - POST /links               -- htmx form target; HTML response
//   - GET  /recent              -- htmx "Load more" pagination fragment
//   - GET  /links/:code/badges  -- htmx polling fragment (clicks + expiry)
//   - GET  /static/*            -- embedded css/js
//
// `POST /links` exists separately from `POST /api/v1/links` because
// htmx submits `application/x-www-form-urlencoded` and expects an HTML
// fragment back; both paths share `Links.Persist` underneath.
//
// `GET /links/:code/badges` is what each row in the recent list polls
// every few seconds to refresh its click count + expiry text without
// touching the rest of the page (so cursor-paginated rows that were
// appended via Load more are not clobbered).
//
// No CSRF protection is applied: the service has no auth concept, so
// there are no per-user mutations to forge -- the create endpoint is
// effectively a public API. Revisit if accounts / quotas are added.
func (w *Web) Mount(e *echo.Echo, staticFS fs.FS) {
	e.GET("/", w.Index)
	e.POST("/links", w.Create)
	e.GET("/recent", w.LoadMore)
	e.GET("/links/:code/badges", w.Badges)

	// Static assets are content-addressed only by filename for now, so we
	// can't use immutable+long-max-age safely. A modest max-age plus
	// must-revalidate keeps the round-trip cheap (304 on If-Modified-Since
	// is moot because embed.FS reports a zero modtime, but browsers still
	// honour the freshness window).
	staticHandler := http.StripPrefix("/static/", http.FileServer(http.FS(staticFS)))
	e.GET("/static/*", echo.WrapHandler(staticCacheHeaders(staticHandler)))
}

// staticCacheHeaders sets a conservative Cache-Control on every static
// response. 1h is short enough that a CSS/JS rebuild propagates quickly
// in dev, long enough to skip the network on intra-session navigation.
func staticCacheHeaders(next http.Handler) http.Handler {
	const maxAge = time.Hour
	header := "public, max-age=" + strconv.Itoa(int(maxAge.Seconds()))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", header)
		next.ServeHTTP(w, r)
	})
}

// --- handlers ---------------------------------------------------------------

// Index renders the landing page with the form and the first page of the
// recent-links list straight from the database.
func (w *Web) Index(c *echo.Context) error {
	page, cursor := w.fetchRecent(c, 0)
	return w.render(c, http.StatusOK, "layout", indexData{
		Items:      page,
		NextCursor: cursor,
	})
}

// expiresInPresets enumerates the values the form's "Expires" select
// is allowed to submit. Keys map to a relative duration; the empty key
// ("never") means no expiry. Anything outside this set is treated as a
// validation error so a hand-crafted form can't sneak unbounded
// expiries through the HTML route.
var expiresInPresets = map[string]time.Duration{
	"":    0,
	"1h":  time.Hour,
	"1d":  24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
}

// resolveExpiresIn maps a form preset to an absolute *time.Time the
// links service understands. Returns (nil, nil) for the "never" case.
func resolveExpiresIn(preset string) (*time.Time, error) {
	d, ok := expiresInPresets[preset]
	if !ok {
		return nil, errors.New("invalid expiry option")
	}
	if d == 0 {
		return nil, nil
	}
	t := time.Now().Add(d)
	return &t, nil
}

// Create handles the HTMX form submission. Returns either a success
// partial (with an OOB recent-list refresh) or an error partial.
func (w *Web) Create(c *echo.Context) error {
	target := strings.TrimSpace(c.FormValue("target_url"))
	userCode := strings.TrimSpace(c.FormValue("code"))
	expiresAt, err := resolveExpiresIn(strings.TrimSpace(c.FormValue("expires_in")))
	if err != nil {
		return w.renderError(c, http.StatusUnprocessableEntity, err.Error())
	}

	link, created, err := w.links.Persist(c.Request().Context(), target, userCode, expiresAt)
	switch kind, status, msg := w.links.ClassifyPersistError("web: create", err); kind {
	case PersistErrNone:
		// fall through to the success render below
	case PersistErrValidation:
		return w.renderError(c, status, msg)
	case PersistErrCodeTaken:
		return w.renderError(c, status, "That code is already in use.")
	case PersistErrInternal:
		return w.renderError(c, status, "Something went wrong. Try again.")
	}

	// Re-fetch the first page so the OOB swap inside link-result.html
	// reflects the link we just created (or surfaced from dedup).
	page, cursor := w.fetchRecent(c, 0)
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	return w.render(c, status, "link-result", linkResultData{
		Link:       w.links.Response(link),
		Items:      page,
		NextCursor: cursor,
	})
}

// Badges handles the per-row HTMX polling request. It returns just the
// badges fragment (`<span>...</span>` containing click count and expiry
// labels) so each visible row can refresh those two pieces of metadata
// in place every few seconds.
//
// Cache-Control: no-store keeps an intermediary (CDN, browser back/forward
// cache) from freezing a stale value -- the whole point of the endpoint
// is that a click that just happened becomes visible on the next poll.
//
// Unknown / invalid codes return 204 No Content rather than 404 so HTMX
// silently swaps in an empty fragment (which collapses the badges) and
// stops; a 4xx would surface as an htmx:responseError.
func (w *Web) Badges(c *echo.Context) error {
	code := c.Param("code")
	link, err := w.links.Resolve(c.Request().Context(), code)
	if errors.Is(err, store.ErrNotFound) {
		return c.NoContent(http.StatusNoContent)
	}
	if err != nil {
		w.logger.Error("web: badges lookup failed", "error", err, "code", code)
		return c.NoContent(http.StatusInternalServerError)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return w.render(c, http.StatusOK, "recent-badges", w.links.Response(link))
}

// LoadMore handles the HTMX "Load more" button. Returns a fragment
// containing the next page's <li>s and an OOB swap that updates the
// pagination control with a fresh cursor (or removes it on the last page).
func (w *Web) LoadMore(c *echo.Context) error {
	before, _ := strconv.ParseInt(c.QueryParam("before"), 10, 64)
	if before < 0 {
		before = 0
	}
	page, cursor := w.fetchRecent(c, before)
	return w.render(c, http.StatusOK, "recent-page", recentPageData{
		Items:      page,
		NextCursor: cursor,
	})
}

// --- rendering --------------------------------------------------------------

type indexData struct {
	Items      []LinkResponse
	NextCursor int64
}

type linkResultData struct {
	Link       LinkResponse
	Items      []LinkResponse
	NextCursor int64
}

type recentPageData struct {
	Items      []LinkResponse
	NextCursor int64
}

// render executes the named template into a buffer first, then writes
// the response. Buffering means a template error (missing field, broken
// nested template, etc.) surfaces *before* any bytes hit the wire, so we
// can still escalate to a 500 instead of leaving the client with a
// half-rendered partial. The HTML payloads here are tiny (~few KB), so
// the extra allocation is irrelevant.
func (w *Web) render(c *echo.Context, status int, name string, data any) error {
	var buf bytes.Buffer
	if err := w.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		w.logger.Error("web: render template", "template", name, "error", err)
		// Fall through to a plain-text 500 -- attempting to render
		// link-error here could trigger the same failure mode.
		return c.String(http.StatusInternalServerError, "internal error")
	}
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	c.Response().WriteHeader(status)
	_, err := c.Response().Write(buf.Bytes())
	return err
}

func (w *Web) renderError(c *echo.Context, status int, msg string) error {
	return w.render(c, status, "link-error", struct{ Error string }{Error: msg})
}

// fetchRecent loads one page of recent links via Links.List and converts
// them to the public response shape. Errors are logged and surfaced as an
// empty page so a flaking database doesn't hide the form behind a 500.
func (w *Web) fetchRecent(c *echo.Context, beforeID int64) ([]LinkResponse, int64) {
	rows, cursor, err := w.links.List(c.Request().Context(), recentPageSize, beforeID)
	if err != nil {
		w.logger.Warn("web: list recent failed", "error", err)
		return nil, 0
	}
	out := make([]LinkResponse, len(rows))
	for i, l := range rows {
		out[i] = w.links.Response(l)
	}
	return out, cursor
}
