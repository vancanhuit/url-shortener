package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"

	"github.com/vancanhuit/url-shortener/internal/shortener"
	"github.com/vancanhuit/url-shortener/internal/store"
)

// listDefaultPageSize is the default page size for `GET /api/v1/links`
// when the client doesn't pass a `limit` query parameter. Tuned for
// the web UI's recent-list (which fetches the first page on load):
// large enough to fill a typical viewport, small enough that the
// payload + Postgres scan stay cheap.
const listDefaultPageSize = 10

// listMaxPageSize is the upper bound enforced on the `limit` query
// parameter. Higher values are silently clamped down rather than
// rejected so a curious caller doesn't get a 422 for asking for "all
// of them"; the sentinel keeps a runaway client from forcing the
// server to materialize a multi-MB page.
const listMaxPageSize = 100

// Create implements POST /api/v1/links.
func (h *Links) Create(c *echo.Context) error {
	var req createReq
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp(ErrCodeInvalidJSONBody, "invalid json body"))
	}

	link, created, err := h.Persist(c.Request().Context(), req.TargetURL, req.Code, req.ExpiresAt)
	switch kind, status, msg := h.ClassifyPersistError("links: create", err); kind {
	case PersistErrNone:
		// fall through to the success response below
	case PersistErrValidation:
		return c.JSON(status, errResp(ErrCodeValidation, msg))
	case PersistErrCodeTaken:
		return c.JSON(status, errResp(ErrCodeCodeTaken, "code already in use"))
	case PersistErrInternal:
		return c.JSON(status, errResp(ErrCodeInternal, "internal error"))
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	return c.JSON(status, h.makeResp(link))
}

// Get implements GET /api/v1/links/:code. Expired links return 410 Gone
// rather than the response body so clients can distinguish a once-valid
// code from an unknown one (and so an expired link's metadata --
// click_count, created_at -- doesn't keep leaking forever).
func (h *Links) Get(c *echo.Context) error {
	code := c.Param("code")
	if !shortener.ValidCode(code) {
		return c.JSON(http.StatusNotFound, errResp(ErrCodeNotFound, "not found"))
	}
	link, err := h.store.GetLinkByCode(c.Request().Context(), nil, code)
	if errors.Is(err, store.ErrNotFound) {
		return c.JSON(http.StatusNotFound, errResp(ErrCodeNotFound, "not found"))
	}
	if err != nil {
		h.logger.Error("links: get failed", "error", err, "code", code)
		return c.JSON(http.StatusInternalServerError, errResp(ErrCodeInternal, "internal error"))
	}
	if link.IsDeleted() {
		return c.JSON(http.StatusGone, errResp(ErrCodeLinkDeleted, "link has been deleted"))
	}
	if link.IsExpired() {
		return c.JSON(http.StatusGone, errResp(ErrCodeLinkExpired, "link has expired"))
	}
	return c.JSON(http.StatusOK, h.makeResp(link))
}

// List implements GET /api/v1/links. Returns up to `limit` links
// ordered newest-first plus an opaque cursor for the next page.
//
// Query parameters:
//
//   - limit  -- page size, defaulted to listDefaultPageSize and clamped
//     to listMaxPageSize. Negative or non-numeric values fall back to
//     the default rather than failing the request.
//   - before -- exclusive lower bound on the row id; pass the previous
//     page's `next_cursor` to walk backwards in time. 0 / missing
//     means "first page".
//
// Soft-deleted and expired rows are excluded by the underlying store
// query, so a busy site that prunes regularly still pages predictably.
// The handler never returns 4xx for a syntactically valid request --
// an unknown `before` cursor simply yields an empty page.
func (h *Links) List(c *echo.Context) error {
	limit := listDefaultPageSize
	if raw := c.QueryParam("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > listMaxPageSize {
		limit = listMaxPageSize
	}

	var beforeID int64
	if raw := c.QueryParam("before"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			beforeID = n
		}
	}

	rows, cursor, err := h.listPage(c.Request().Context(), limit, beforeID)
	if err != nil {
		h.logger.Error("links: list", "error", err)
		return c.JSON(http.StatusInternalServerError,
			errResp(ErrCodeInternal, "internal error"))
	}

	items := make([]LinkResponse, len(rows))
	for i, l := range rows {
		items[i] = h.makeResp(l)
	}

	resp := ListResponse{Items: items}
	if cursor > 0 {
		// Encode "no more rows" as JSON null rather than 0 so clients
		// can branch on a single nullability check without smuggling a
		// magic number in the contract.
		next := cursor
		resp.NextCursor = &next
	}
	return c.JSON(http.StatusOK, resp)
}

// Delete implements DELETE /api/v1/links/:code. It performs a
// soft-delete: the row stays in `links` with its audit columns
// intact, but `deleted_at` is stamped so the redirect, lookup, list,
// and dedup paths all stop seeing it. The cache entry is invalidated
// best-effort so a subsequent /r/:code goes to the store and surfaces
// a 410.
//
// Idempotency is semantic, not response-shape: the first DELETE
// against a live code returns 204; a second DELETE against the same
// (now-deleted) code returns 404, the same response the API gives
// for any other unknown / unreachable code. Clients that need to
// treat both first-and-second-DELETE as success can collapse 204 +
// 404 themselves.
func (h *Links) Delete(c *echo.Context) error {
	code := c.Param("code")
	if !shortener.ValidCode(code) {
		return c.JSON(http.StatusNotFound, errResp(ErrCodeNotFound, "not found"))
	}
	ctx := c.Request().Context()
	err := h.store.SoftDeleteLink(ctx, nil, code)
	if errors.Is(err, store.ErrNotFound) {
		return c.JSON(http.StatusNotFound, errResp(ErrCodeNotFound, "not found"))
	}
	if err != nil {
		h.logger.Error("links: delete failed", "error", err, "code", code)
		return c.JSON(http.StatusInternalServerError, errResp(ErrCodeInternal, "internal error"))
	}
	// Best-effort cache invalidation: a stale entry would only
	// last `cacheTTL` anyway, but eagerly removing it makes the
	// post-delete /r/:code 410 immediately rather than after the
	// TTL elapses. Failures are logged and otherwise ignored --
	// the soft-delete itself has already committed.
	if err := h.cache.Del(ctx, cacheKey(code)); err != nil {
		h.logger.Warn("links: cache del failed after delete", "error", err, "code", code)
	}
	return c.NoContent(http.StatusNoContent)
}

// Redirect implements GET /r/:code. Tries the cache first, falls back
// to the store, and back-fills the cache on a hit. Expired links return
// 410 Gone. Successful redirects fire-and-forget a click increment so
// the UPDATE never delays the 302.
//
// A cache hit is always served as a redirect: cache TTL is clamped to
// the link's remaining lifetime in cachePut, so anything that survives
// in the cache is by construction not yet expired.
func (h *Links) Redirect(c *echo.Context) error {
	code := c.Param("code")
	if !shortener.ValidCode(code) {
		return c.JSON(http.StatusNotFound, errResp(ErrCodeNotFound, "not found"))
	}
	ctx := c.Request().Context()

	switch target, hit, negative := h.cacheGet(ctx, code); {
	case hit && negative:
		// Cached "known dead-end" -- absorbs scanning traffic
		// without hitting the store. The original status (404 vs
		// 410) isn't preserved because the public response surface
		// for both is the same as far as a redirect client is
		// concerned: there is nothing here to redirect to.
		return c.JSON(http.StatusNotFound, errResp(ErrCodeNotFound, "not found"))
	case hit:
		h.recordClick(code)
		return c.Redirect(http.StatusFound, target)
	}

	link, err := h.store.GetLinkByCode(ctx, nil, code)
	if errors.Is(err, store.ErrNotFound) {
		h.cachePutNegative(ctx, code)
		return c.JSON(http.StatusNotFound, errResp(ErrCodeNotFound, "not found"))
	}
	if err != nil {
		h.logger.Error("links: redirect lookup failed", "error", err, "code", code)
		return c.JSON(http.StatusInternalServerError, errResp(ErrCodeInternal, "internal error"))
	}
	if link.IsDeleted() {
		h.cachePutNegative(ctx, code)
		return c.JSON(http.StatusGone, errResp(ErrCodeLinkDeleted, "link has been deleted"))
	}
	if link.IsExpired() {
		h.cachePutNegative(ctx, code)
		return c.JSON(http.StatusGone, errResp(ErrCodeLinkExpired, "link has expired"))
	}

	h.cachePut(ctx, link)
	h.recordClick(link.Code)
	return c.Redirect(http.StatusFound, link.TargetURL)
}

func (h *Links) makeResp(l store.Link) LinkResponse {
	return LinkResponse{
		Code:       l.Code,
		ShortURL:   h.baseURL + "/r/" + l.Code,
		TargetURL:  l.TargetURL,
		CreatedAt:  l.CreatedAt,
		ClickCount: l.ClickCount,
		ExpiresAt:  l.ExpiresAt,
	}
}
