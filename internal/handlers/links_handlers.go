package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/vancanhuit/url-shortener/api"
	"github.com/vancanhuit/url-shortener/internal/shortener"
	"github.com/vancanhuit/url-shortener/internal/store"
)

// listDefaultPageSize is the default page size for GET /api/v1/links.
const listDefaultPageSize = 10

// listMaxPageSize is the upper bound enforced on the `limit` query parameter.
// Higher values are silently clamped rather than rejected.
const listMaxPageSize = 100

// CreateLink implements api.StrictServerInterface.
func (h *Links) CreateLink(ctx context.Context, req api.CreateLinkRequestObject) (api.CreateLinkResponseObject, error) {
	var userCode string
	if req.Body.Code != nil {
		userCode = *req.Body.Code
	}
	link, created, err := h.Persist(ctx, req.Body.TargetURL, userCode, req.Body.ExpiresAt)
	switch kind, _, msg := h.ClassifyPersistError("links: create", err); kind {
	case PersistErrNone:
		// fall through
	case PersistErrValidation:
		return api.CreateLink422JSONResponse(errResp(api.ErrorResponseCodeValidationFailed, msg)), nil
	case PersistErrCodeTaken:
		return api.CreateLink409JSONResponse(errResp(api.ErrorResponseCodeCodeTaken, "code already in use")), nil
	default:
		return api.CreateLink500JSONResponse{InternalErrorJSONResponse: api.InternalErrorJSONResponse(errResp(api.ErrorResponseCodeInternalError, "internal error"))}, nil
	}
	resp := h.makeResp(link)
	if created {
		h.metrics.IncShorten(ShortenCreated)
		return api.CreateLink201JSONResponse(resp), nil
	}
	h.metrics.IncShorten(ShortenDeduped)
	return api.CreateLink200JSONResponse(resp), nil
}

// GetLink implements api.StrictServerInterface.
func (h *Links) GetLink(ctx context.Context, req api.GetLinkRequestObject) (api.GetLinkResponseObject, error) {
	if !shortener.ValidCode(req.Code) {
		return api.GetLink404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse(errResp(api.ErrorResponseCodeNotFound, "not found"))}, nil
	}
	link, err := h.store.GetLinkByCode(ctx, nil, req.Code)
	if errors.Is(err, store.ErrNotFound) {
		return api.GetLink404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse(errResp(api.ErrorResponseCodeNotFound, "not found"))}, nil
	}
	if err != nil {
		h.logger.Error("links: get failed", "error", err, "code", req.Code)
		return api.GetLink500JSONResponse{InternalErrorJSONResponse: api.InternalErrorJSONResponse(errResp(api.ErrorResponseCodeInternalError, "internal error"))}, nil
	}
	if link.IsDeleted() {
		return api.GetLink410JSONResponse(errResp(api.ErrorResponseCodeLinkDeleted, "link has been deleted")), nil
	}
	if link.IsExpired() {
		return api.GetLink410JSONResponse(errResp(api.ErrorResponseCodeLinkExpired, "link has expired")), nil
	}
	return api.GetLink200JSONResponse(h.makeResp(link)), nil
}

// ListLinks implements api.StrictServerInterface.
func (h *Links) ListLinks(ctx context.Context, req api.ListLinksRequestObject) (api.ListLinksResponseObject, error) {
	limit := listDefaultPageSize
	if req.Params.Limit != nil && *req.Params.Limit > 0 {
		limit = *req.Params.Limit
	}
	if limit > listMaxPageSize {
		limit = listMaxPageSize
	}
	var beforeID int64
	if req.Params.Before != nil && *req.Params.Before > 0 {
		beforeID = *req.Params.Before
	}

	rows, cursor, err := h.listPage(ctx, limit, beforeID)
	if err != nil {
		h.logger.Error("links: list", "error", err)
		return api.ListLinks500JSONResponse{InternalErrorJSONResponse: api.InternalErrorJSONResponse(errResp(api.ErrorResponseCodeInternalError, "internal error"))}, nil
	}
	items := make([]api.LinkResponse, len(rows))
	for i, l := range rows {
		items[i] = h.makeResp(l)
	}
	resp := api.ListLinks200JSONResponse{Items: items}
	if cursor > 0 {
		next := cursor
		resp.NextCursor = &next
	}
	return resp, nil
}

// DeleteLink implements api.StrictServerInterface.
func (h *Links) DeleteLink(ctx context.Context, req api.DeleteLinkRequestObject) (api.DeleteLinkResponseObject, error) {
	if !shortener.ValidCode(req.Code) {
		return api.DeleteLink404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse(errResp(api.ErrorResponseCodeNotFound, "not found"))}, nil
	}
	err := h.store.SoftDeleteLink(ctx, nil, req.Code)
	if errors.Is(err, store.ErrNotFound) {
		return api.DeleteLink404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse(errResp(api.ErrorResponseCodeNotFound, "not found"))}, nil
	}
	if err != nil {
		h.logger.Error("links: delete failed", "error", err, "code", req.Code)
		return api.DeleteLink500JSONResponse{InternalErrorJSONResponse: api.InternalErrorJSONResponse(errResp(api.ErrorResponseCodeInternalError, "internal error"))}, nil
	}
	// Best-effort cache invalidation so the next /r/:code surfaces 410
	// immediately rather than waiting out the TTL.
	if err := h.cache.Del(ctx, cacheKey(req.Code)); err != nil {
		h.logger.Warn("links: cache del failed after delete", "error", err, "code", req.Code)
	}
	return api.DeleteLink204Response{}, nil
}

// Redirect implements GET /r/{code}. Plain http handler (not strict).
func (h *Links) Redirect(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	if !shortener.ValidCode(code) {
		h.metrics.IncRedirect(RedirectNotFound)
		writeJSON(w, http.StatusNotFound, errResp(api.ErrorResponseCodeNotFound, "not found"))
		return
	}
	ctx := r.Context()

	target, hit, negative := h.cacheGet(ctx, code)
	if hit && negative {
		h.metrics.IncRedirect(RedirectNegativeHit)
		writeJSON(w, http.StatusNotFound, errResp(api.ErrorResponseCodeNotFound, "not found"))
		return
	}
	if hit {
		h.metrics.IncRedirect(RedirectCacheHit)
		h.recordClick(code)                           //nolint:contextcheck // goroutine outlives the request; uses its own timeout context
		http.Redirect(w, r, target, http.StatusFound) //nolint:gosec // target is populated exclusively from DB-validated URLs (http/https, non-private hosts); the cache is internal, not user-controlled
		return
	}

	link, err := h.store.GetLinkByCode(ctx, nil, code)
	if errors.Is(err, store.ErrNotFound) {
		h.cachePutNegative(ctx, code)
		h.metrics.IncRedirect(RedirectNotFound)
		writeJSON(w, http.StatusNotFound, errResp(api.ErrorResponseCodeNotFound, "not found"))
		return
	}
	if err != nil {
		h.logger.Error("links: redirect lookup failed", "error", err, "code", code)
		h.metrics.IncRedirect(RedirectError)
		writeJSON(w, http.StatusInternalServerError, errResp(api.ErrorResponseCodeInternalError, "internal error"))
		return
	}
	if link.IsDeleted() {
		h.cachePutNegative(ctx, code)
		h.metrics.IncRedirect(RedirectGone)
		writeJSON(w, http.StatusGone, errResp(api.ErrorResponseCodeLinkDeleted, "link has been deleted"))
		return
	}
	if link.IsExpired() {
		h.cachePutNegative(ctx, code)
		h.metrics.IncRedirect(RedirectGone)
		writeJSON(w, http.StatusGone, errResp(api.ErrorResponseCodeLinkExpired, "link has expired"))
		return
	}

	h.cachePut(ctx, link)
	h.metrics.IncRedirect(RedirectStoreHit)
	h.recordClick(link.Code) //nolint:contextcheck // goroutine outlives the request; uses its own timeout context
	http.Redirect(w, r, link.TargetURL, http.StatusFound)
}

func (h *Links) makeResp(l store.Link) api.LinkResponse {
	return api.LinkResponse{
		Code:       l.Code,
		ShortURL:   h.baseURL + "/r/" + l.Code,
		TargetURL:  l.TargetURL,
		CreatedAt:  l.CreatedAt,
		ClickCount: l.ClickCount,
		ExpiresAt:  l.ExpiresAt,
	}
}
