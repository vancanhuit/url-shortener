// This file implements the JSON API for links and the public short-URL
// redirect handler:
//
//	POST /api/v1/links            create a link (auto or user-supplied code)
//	GET  /api/v1/links/:code      fetch the link metadata as JSON
//	GET  /:code                   302 redirect to the target URL
//
// Reads use a read-through cache when one is configured; cache failures
// are non-fatal and logged at the call site.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/vancanhuit/url-shortener/internal/shortener"
	"github.com/vancanhuit/url-shortener/internal/store"
)

// Tunables for the links handler. CacheTTL bounds how stale a redirect
// lookup can be; CreateMaxRetries caps the auto-generated-code retry loop
// so a degenerate Postgres state can't pin a request forever.
const (
	CacheTTL         = 1 * time.Hour
	CreateMaxRetries = 5
	TargetURLMaxLen  = 2048
)

// LinkStore is the storage surface the links handler depends on. It is
// satisfied by *store.Store; tests use a fake implementation.
type LinkStore interface {
	CreateLink(ctx context.Context, db store.DBTX, code, targetURL string) (store.Link, error)
	GetLinkByCode(ctx context.Context, db store.DBTX, code string) (store.Link, error)
}

// LinkCache is the cache surface the links handler depends on. It is
// satisfied by *cache.Client. Get returns (value, true, nil) on hit.
type LinkCache interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	Del(ctx context.Context, keys ...string) error
}

// Generator is what the handler uses to mint new codes; *shortener.Generator
// satisfies it. Behind an interface so tests can inject a deterministic
// stub (e.g. for collision-loop coverage).
type Generator interface {
	Generate() (string, error)
}

// Links is the handler bundle for the API endpoints. Both store and cache
// are required: the cache is on the redirect hot path and the service
// configuration enforces a non-empty URL_SHORTENER_REDIS_URL.
type Links struct {
	store    LinkStore
	cache    LinkCache
	gen      Generator
	baseURL  string
	logger   *slog.Logger
	cacheTTL time.Duration
	retries  int
}

// LinksConfig groups Links' constructor arguments. Store, Cache, and
// Generator are all required; baseURL is the public origin used when
// rendering `short_url` in responses; Logger defaults to slog.Default.
type LinksConfig struct {
	Store     LinkStore
	Cache     LinkCache
	Generator Generator
	BaseURL   string
	Logger    *slog.Logger
}

// NewLinks builds a Links handler. Required: Store, Generator, BaseURL.
func NewLinks(cfg LinksConfig) *Links {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Links{
		store:    cfg.Store,
		cache:    cfg.Cache,
		gen:      cfg.Generator,
		baseURL:  strings.TrimRight(cfg.BaseURL, "/"),
		logger:   logger,
		cacheTTL: CacheTTL,
		retries:  CreateMaxRetries,
	}
}

// Mount registers the API + redirect routes on e. The public redirect
// lives under `/r/:code` (rather than `/:code`) so it can never shadow
// operational endpoints (`/healthz`, `/readyz`, `/version`) or future
// top-level routes like `/api`, static assets, or HTML pages.
func (h *Links) Mount(e *echo.Echo) {
	e.POST("/api/v1/links", h.Create)
	e.GET("/api/v1/links/:code", h.Get)
	e.GET("/r/:code", h.Redirect)
}

// --- request / response shapes ----------------------------------------------

type createReq struct {
	TargetURL string `json:"target_url"`
	Code      string `json:"code,omitempty"`
}

// LinkResponse is the JSON shape returned by Create and Get.
type LinkResponse struct {
	Code      string    `json:"code"`
	ShortURL  string    `json:"short_url"`
	TargetURL string    `json:"target_url"`
	CreatedAt time.Time `json:"created_at"`
}

// ErrorResponse is the JSON shape returned for any non-2xx response.
type ErrorResponse struct {
	Error string `json:"error"`
}

// --- handlers ---------------------------------------------------------------

// Create implements POST /api/v1/links.
func (h *Links) Create(c *echo.Context) error {
	var req createReq
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
		return c.JSON(http.StatusBadRequest, ErrorResponse{Error: "invalid json body"})
	}
	if err := validateTargetURL(req.TargetURL); err != nil {
		return c.JSON(http.StatusUnprocessableEntity, ErrorResponse{Error: err.Error()})
	}

	ctx := c.Request().Context()
	var (
		link store.Link
		err  error
	)
	switch {
	case req.Code != "":
		if !shortener.ValidCode(req.Code) {
			return c.JSON(http.StatusUnprocessableEntity, ErrorResponse{
				Error: fmt.Sprintf("code must be %d-%d base62 characters",
					shortener.MinLength, shortener.MaxLength),
			})
		}
		link, err = h.store.CreateLink(ctx, nil, req.Code, req.TargetURL)
		if errors.Is(err, store.ErrCodeTaken) {
			return c.JSON(http.StatusConflict, ErrorResponse{Error: "code already in use"})
		}
	default:
		link, err = h.createWithRandomCode(ctx, req.TargetURL)
	}
	if err != nil {
		h.logger.Error("links: create failed", "error", err)
		return c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
	}

	h.cachePut(ctx, link)
	return c.JSON(http.StatusCreated, h.makeResp(link))
}

// createWithRandomCode generates a fresh code and retries on the rare
// unique-collision. After h.retries failed attempts it gives up: that
// implies either an exhausted keyspace or a degenerate generator and
// should surface as a 500.
func (h *Links) createWithRandomCode(ctx context.Context, target string) (store.Link, error) {
	for i := 0; i < h.retries; i++ {
		code, err := h.gen.Generate()
		if err != nil {
			return store.Link{}, fmt.Errorf("generate code: %w", err)
		}
		l, err := h.store.CreateLink(ctx, nil, code, target)
		if errors.Is(err, store.ErrCodeTaken) {
			h.logger.Warn("links: code collision; retrying", "attempt", i+1, "code", code)
			continue
		}
		return l, err
	}
	return store.Link{}, fmt.Errorf("failed to generate unique code after %d attempts", h.retries)
}

// Get implements GET /api/v1/links/:code.
func (h *Links) Get(c *echo.Context) error {
	code := c.Param("code")
	if !shortener.ValidCode(code) {
		return c.JSON(http.StatusNotFound, ErrorResponse{Error: "not found"})
	}
	link, err := h.store.GetLinkByCode(c.Request().Context(), nil, code)
	if errors.Is(err, store.ErrNotFound) {
		return c.JSON(http.StatusNotFound, ErrorResponse{Error: "not found"})
	}
	if err != nil {
		h.logger.Error("links: get failed", "error", err, "code", code)
		return c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
	}
	return c.JSON(http.StatusOK, h.makeResp(link))
}

// Redirect implements GET /:code. Tries the cache first, falls back to
// the store, and back-fills the cache on a hit.
func (h *Links) Redirect(c *echo.Context) error {
	code := c.Param("code")
	if !shortener.ValidCode(code) {
		return echo.NewHTTPError(http.StatusNotFound, "not found")
	}
	ctx := c.Request().Context()

	if target, ok := h.cacheGet(ctx, code); ok {
		return c.Redirect(http.StatusFound, target)
	}

	link, err := h.store.GetLinkByCode(ctx, nil, code)
	if errors.Is(err, store.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "not found")
	}
	if err != nil {
		h.logger.Error("links: redirect lookup failed", "error", err, "code", code)
		return echo.NewHTTPError(http.StatusInternalServerError, "internal error")
	}

	h.cachePut(ctx, link)
	return c.Redirect(http.StatusFound, link.TargetURL)
}

// --- cache helpers ----------------------------------------------------------
//
// All cache failures are logged and swallowed: the cache is on the hot path
// for redirects but it is an optimisation, not a source of truth, so a
// transient outage degrades to a store-only round-trip rather than 5xx.

func (h *Links) cacheGet(ctx context.Context, code string) (string, bool) {
	v, ok, err := h.cache.Get(ctx, cacheKey(code))
	if err != nil {
		h.logger.Warn("links: cache get failed", "error", err, "code", code)
		return "", false
	}
	return v, ok
}

func (h *Links) cachePut(ctx context.Context, l store.Link) {
	if err := h.cache.Set(ctx, cacheKey(l.Code), l.TargetURL, h.cacheTTL); err != nil {
		h.logger.Warn("links: cache set failed", "error", err, "code", l.Code)
	}
}

func cacheKey(code string) string { return "link:" + code }

// --- helpers ----------------------------------------------------------------

func (h *Links) makeResp(l store.Link) LinkResponse {
	return LinkResponse{
		Code:      l.Code,
		ShortURL:  h.baseURL + "/r/" + l.Code,
		TargetURL: l.TargetURL,
		CreatedAt: l.CreatedAt,
	}
}

// validateTargetURL enforces the rules the API contract advertises:
// non-empty, length-capped, parseable, http(s) scheme, non-empty host.
func validateTargetURL(s string) error {
	if s == "" {
		return errors.New("target_url is required")
	}
	if len(s) > TargetURLMaxLen {
		return fmt.Errorf("target_url exceeds %d characters", TargetURLMaxLen)
	}
	u, err := url.Parse(s)
	if err != nil {
		return errors.New("target_url is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("target_url must use http or https")
	}
	if u.Host == "" {
		return errors.New("target_url must have a host")
	}
	return nil
}
