// Package handlers implements the JSON API for links and the public
// short-URL redirect handler.
//
// The implementation is split across several files:
//   - links.go           interfaces, struct, constructor, route mounting
//   - links_types.go     helper functions, error types
//   - links_handlers.go  HTTP handlers (StrictServerInterface + Redirect)
//   - links_service.go   business logic (Persist, ClassifyPersistError, etc.)
//   - links_cache.go     cache helpers and background click machinery
package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vancanhuit/url-shortener/api"
	"github.com/vancanhuit/url-shortener/internal/store"
)

// Tunables for the links handler. CacheTTL bounds how stale a redirect
// lookup can be; CreateMaxRetries caps the auto-generated-code retry loop
// so a degenerate Postgres state cannot pin a request forever.
const (
	CacheTTL         = 1 * time.Hour
	CreateMaxRetries = 5
	TargetURLMaxLen  = 2048

	// NegativeCacheTTL bounds how long a "not found / gone" answer
	// for /r/:code is held in Redis before we re-check Postgres.
	// Short on purpose: it exists to absorb scanning attacks (a few
	// thousand misses per second on random codes) without amplifying
	// load on the DB, not to mask legitimate state changes. A custom
	// code that 404s now and is created via POST in the next minute
	// will start resolving as soon as the entry expires; the Persist
	// path also overwrites the negative entry on success so the
	// observable lag is normally well under the TTL.
	NegativeCacheTTL = 30 * time.Second

	// cacheNegativeSentinel is the value stored under link:<code>
	// to mean "this code is known to be unresolvable". The empty
	// string is unambiguous: validateTargetURL rejects empty inputs
	// so a real link never round-trips with this value.
	cacheNegativeSentinel = ""

	// clickTimeout caps how long a fire-and-forget click increment
	// goroutine may run. Long enough to absorb a momentary stall on
	// the database, short enough that a permanently degraded DB
	// cannot pin goroutines around forever under sustained traffic.
	clickTimeout = 5 * time.Second

	// Click-increment retry schedule for transient DB failures
	// (deadlock_detected etc.). The increment is detached, so the
	// retries do not add user-visible latency; the budget below stays
	// well inside clickTimeout even at the cap.
	//
	//   attempt 1: immediate
	//   attempt 2: ~50ms +/- 25ms
	//   attempt 3: ~100ms +/- 50ms
	//   attempt 4: ~200ms +/- 100ms
	//   attempt 5: ~400ms +/- 200ms  (worst case ~750ms total)
	clickRetryAttempts   = 5
	clickRetryBaseDelay  = 50 * time.Millisecond
	clickRetryMaxBackoff = 1 * time.Second

	// clockSkewGrace is the leniency applied when validating a
	// caller-supplied expires_at: a few seconds in the past is
	// accepted to absorb honest skew between a client's wall clock
	// and the server's. Anything further back is rejected as a
	// clearly invalid input rather than silently treated as zero.
	clockSkewGrace = 30 * time.Second
)

// LinkStore is the storage surface the links handler depends on. It is
// satisfied by *store.Store; tests use a fake implementation.
//
// The db parameter on every method follows the nil-means-pool convention
// defined on store.DBTX: pass nil for standalone operations (all current
// call sites do this) and a live *pgx.Tx only when multiple operations
// must share a transaction.
type LinkStore interface {
	CreateLink(ctx context.Context, db store.DBTX, code, targetURL string, expiresAt *time.Time) (store.Link, error)
	// CreateAutoLink atomically inserts a permanent auto-generated link or
	// returns the existing one when a live permanent row already covers
	// targetURL (dedup hit). created is false on a dedup hit. ErrCodeTaken
	// signals a code collision on a different row; callers should retry
	// with a new code.
	CreateAutoLink(ctx context.Context, db store.DBTX, code, targetURL string) (store.Link, bool, error)
	GetLinkByCode(ctx context.Context, db store.DBTX, code string) (store.Link, error)
	ListLinks(ctx context.Context, db store.DBTX, limit int, beforeID int64) ([]store.Link, error)
	IncrementClicks(ctx context.Context, db store.DBTX, code string) error
	SoftDeleteLink(ctx context.Context, db store.DBTX, code string) error
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
	store       LinkStore
	cache       LinkCache
	gen         Generator
	baseURL     string
	logger      *slog.Logger
	metrics     Metrics
	cacheTTL    time.Duration
	negCacheTTL time.Duration

	// bgWG tracks fire-and-forget goroutines (currently just the
	// click-increment background work). Tests use
	// WaitForBackgroundTasks to wait on it; production never observes
	// it directly today but the hook is in place for a future graceful
	// shutdown that wants to drain in-flight clicks.
	bgWG sync.WaitGroup
}

// LinksConfig groups Links' constructor arguments. Store, Cache, and
// Generator are all required; BaseURL is the public origin used when
// rendering short_url in responses; Logger defaults to slog.Default.
// CacheTTL and NegativeCacheTTL are optional: zero means use the
// package-level defaults (CacheTTL and NegativeCacheTTL constants).
type LinksConfig struct {
	Store            LinkStore
	Cache            LinkCache
	Generator        Generator
	BaseURL          string
	Logger           *slog.Logger
	Metrics          Metrics
	CacheTTL         time.Duration
	NegativeCacheTTL time.Duration
}

// NewLinks builds a Links handler. Required: Store, Generator, BaseURL.
func NewLinks(cfg LinksConfig) *Links {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	cacheTTL := cfg.CacheTTL
	if cacheTTL <= 0 {
		cacheTTL = CacheTTL
	}
	negCacheTTL := cfg.NegativeCacheTTL
	if negCacheTTL <= 0 {
		negCacheTTL = NegativeCacheTTL
	}
	metrics := cfg.Metrics
	if metrics == nil {
		metrics = noopMetrics{}
	}
	return &Links{
		store:       cfg.Store,
		cache:       cfg.Cache,
		gen:         cfg.Generator,
		baseURL:     strings.TrimRight(cfg.BaseURL, "/"),
		logger:      logger,
		metrics:     metrics,
		cacheTTL:    cacheTTL,
		negCacheTTL: negCacheTTL,
	}
}

// Mount registers the API + redirect routes on r. The public redirect
// lives under /r/{code} (rather than /{code}) so it can never shadow
// operational endpoints (/livez, /readyz, /version) or future
// top-level routes like /api, static assets, or HTML pages.
//
// createMW are route-scoped middlewares attached only to
// POST /api/v1/links -- the abuse-prone write endpoint. Server.New
// uses this hook to install the rate limiter when configured; tests
// usually pass nothing.
func (h *Links) Mount(r chi.Router, createMW ...func(http.Handler) http.Handler) {
	opts := api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				writeJSON(w, http.StatusRequestEntityTooLarge, errResp(api.ErrorResponseCodeInternalError, "request body too large"))
				return
			}
			writeJSON(w, http.StatusBadRequest, errResp(api.ErrorResponseCodeInvalidJSONBody, "invalid json body"))
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, _ error) {
			writeJSON(w, http.StatusInternalServerError, errResp(api.ErrorResponseCodeInternalError, "internal error"))
		},
	}
	strict := api.NewStrictHandlerWithOptions(h, nil, opts)
	wrapper := api.ServerInterfaceWrapper{
		Handler: strict,
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeJSON(w, http.StatusBadRequest, errResp(api.ErrorResponseCodeValidationFailed, err.Error()))
		},
	}
	r.Get("/api/v1/links", wrapper.ListLinks)
	r.With(createMW...).Post("/api/v1/links", wrapper.CreateLink)
	r.Delete("/api/v1/links/{code}", wrapper.DeleteLink)
	r.Get("/api/v1/links/{code}", wrapper.GetLink)
	r.Get("/r/{code}", h.Redirect)
}
