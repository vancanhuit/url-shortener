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
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
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

	// clickTimeout caps how long a fire-and-forget click increment
	// goroutine may run. Long enough to absorb a momentary stall on
	// the database, short enough that a permanently degraded DB
	// can't pin goroutines around forever under sustained traffic.
	clickTimeout = 5 * time.Second

	// Click-increment retry schedule for transient DB failures
	// (deadlock_detected etc.). The increment is detached, so the
	// retries don't add user-visible latency; the budget below stays
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
type LinkStore interface {
	CreateLink(ctx context.Context, db store.DBTX, code, targetURL string, expiresAt *time.Time) (store.Link, error)
	GetLinkByCode(ctx context.Context, db store.DBTX, code string) (store.Link, error)
	GetLinkByTargetURL(ctx context.Context, db store.DBTX, targetURL string) (store.Link, error)
	ListLinks(ctx context.Context, db store.DBTX, limit int, beforeID int64) ([]store.Link, error)
	IncrementClicks(ctx context.Context, db store.DBTX, code string) error
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

	// bgWG tracks fire-and-forget goroutines (currently just the
	// click-increment background work). Tests use
	// WaitForBackgroundTasks to wait on it; production never observes
	// it directly today but the hook is in place for a future graceful
	// shutdown that wants to drain in-flight clicks.
	bgWG sync.WaitGroup
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
	TargetURL string     `json:"target_url"`
	Code      string     `json:"code,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// LinkResponse is the JSON shape returned by Create and Get.
//
// ExpiresAt is omitted entirely (rather than rendered as null) for the
// common "never expires" case so JSON consumers can distinguish a
// permanent link with a single key check.
type LinkResponse struct {
	Code       string     `json:"code"`
	ShortURL   string     `json:"short_url"`
	TargetURL  string     `json:"target_url"`
	CreatedAt  time.Time  `json:"created_at"`
	ClickCount int64      `json:"click_count"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// ErrorResponse is the JSON shape returned for any non-2xx response from
// the JSON API. The Error field is the human-readable description (safe
// to surface in a UI); Code is a stable machine-readable identifier
// suitable for client-side branching, metric labels, and i18n keys. The
// pair is set together via errResp; callers should never construct an
// ErrorResponse with one field and not the other.
type ErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// API error codes. These strings are part of the public API contract:
// once published, the values must not change (clients may switch on
// them). Adding new codes is fine; renaming or removing an existing one
// is a breaking change and warrants a major-version bump.
const (
	ErrCodeInvalidJSONBody = "invalid_json_body" // 400 on POST when the body is not parseable JSON.
	ErrCodeValidation      = "validation_failed" // 422 when input fails our rules (bad URL, bad code, bad expiry).
	ErrCodeCodeTaken       = "code_taken"        // 409 when a user-supplied short code is already in use.
	ErrCodeNotFound        = "not_found"         // 404 when the requested code does not exist.
	ErrCodeLinkExpired     = "link_expired"      // 410 when the link existed but has passed its expires_at.
	ErrCodeInternal        = "internal_error"    // 500 for any other failure.
)

// errResp builds an ErrorResponse with both fields set. Centralised so
// every call site is forced to provide a code, preventing the public
// shape from drifting back into "human message only".
func errResp(code, msg string) ErrorResponse {
	return ErrorResponse{Error: msg, Code: code}
}

// --- service-level helpers (exposed for the web handler) -------------------

// ValidationError signals a user-input failure that should map to HTTP 422
// (or an inline error in the HTML UI). The Msg is safe to display verbatim.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// PersistErrorKind classifies the failure modes of Links.Persist so
// HTTP layers can drive their response selection from a single switch
// instead of re-implementing the errors.As/errors.Is fork in every
// caller.
type PersistErrorKind int

// PersistError* are the kinds returned by ClassifyPersistError. None
// is the zero value, returned only when the underlying error is nil.
const (
	PersistErrNone PersistErrorKind = iota
	PersistErrValidation
	PersistErrCodeTaken
	PersistErrInternal
)

// ClassifyPersistError maps an error returned by Persist to a kind,
// the HTTP status code matching that kind, and -- for validation
// failures -- the user-facing message extracted from the error. The
// JSON API and the HTML form share this classification but render the
// non-validation copy ("code already in use" vs "That code is already
// in use.") differently, which is why this returns the parts to plug
// into a response rather than a fully-formed reply.
//
// Internal errors are logged here as a side effect (with op as the
// caller-supplied scope label, e.g. "links: create" or "web: create")
// so callers don't have to repeat the slog call site.
func (h *Links) ClassifyPersistError(op string, err error) (kind PersistErrorKind, status int, msg string) {
	if err == nil {
		return PersistErrNone, 0, ""
	}
	var verr *ValidationError
	switch {
	case errors.As(err, &verr):
		return PersistErrValidation, http.StatusUnprocessableEntity, verr.Msg
	case errors.Is(err, store.ErrCodeTaken):
		return PersistErrCodeTaken, http.StatusConflict, ""
	default:
		h.logger.Error(op+": persist failed", "error", err)
		return PersistErrInternal, http.StatusInternalServerError, ""
	}
}

// Persist validates the inputs, normalizes the target URL, and either
// creates a new link or returns an existing one (auto-generated codes
// with no expiry only). The returned errors are typed so callers can
// map them to content-appropriate responses:
//
//   - *ValidationError: bad target_url, code, or expiry -> 422
//   - store.ErrCodeTaken: duplicate user-supplied code  -> 409
//   - any other non-nil error: internal failure         -> 500
//
// expiresAt may be nil for a link that never expires. When non-nil it
// must be in the future. Dedup is suppressed whenever expiresAt is
// non-nil: an ephemeral request should never silently extend the
// lifetime of an existing permanent row, and the store layer already
// excludes expiring rows from the dedup lookup so the converse holds
// too.
//
// The boolean `created` is true when a new row was inserted and false
// when an existing row was reused (which only happens when userCode is
// empty AND expiresAt is nil AND a permanent row already covers the
// normalized target); JSON callers translate this into 201 vs 200.
//
// Note: dedup is best-effort. Two simultaneous requests for the same new
// target may both miss the lookup and both insert; this is no worse than
// today's behaviour and avoids needing a unique constraint that would
// conflict with user-supplied-code semantics.
func (h *Links) Persist(ctx context.Context, target, userCode string, expiresAt *time.Time) (link store.Link, created bool, err error) {
	if err := validateTargetURL(target); err != nil {
		return store.Link{}, false, &ValidationError{Msg: err.Error()}
	}
	norm, err := normalizeURL(target)
	if err != nil {
		return store.Link{}, false, &ValidationError{Msg: err.Error()}
	}
	if err := validateExpiresAt(expiresAt); err != nil {
		return store.Link{}, false, &ValidationError{Msg: err.Error()}
	}

	if userCode != "" {
		if !shortener.ValidCode(userCode) {
			return store.Link{}, false, &ValidationError{
				Msg: fmt.Sprintf("code must be %d-%d base62 characters",
					shortener.MinLength, shortener.MaxLength),
			}
		}
		link, err = h.store.CreateLink(ctx, nil, userCode, norm, expiresAt)
		if err != nil {
			return store.Link{}, false, err
		}
		h.cachePut(ctx, link)
		return link, true, nil
	}

	// Auto-generated path: only dedup permanent requests against
	// permanent existing rows. The store already filters the
	// existing-side condition (expires_at IS NULL); we enforce the
	// requesting-side here.
	if expiresAt == nil {
		existing, lookupErr := h.store.GetLinkByTargetURL(ctx, nil, norm)
		if lookupErr == nil {
			return existing, false, nil
		}
		if !errors.Is(lookupErr, store.ErrNotFound) {
			return store.Link{}, false, lookupErr
		}
	}

	link, err = h.createWithRandomCode(ctx, norm, expiresAt)
	if err != nil {
		return store.Link{}, false, err
	}
	h.cachePut(ctx, link)
	return link, true, nil
}

// Resolve returns the link for code, or store.ErrNotFound for either an
// unknown code or one that fails the syntactic validity check (avoiding a
// pointless DB round-trip for obvious junk).
func (h *Links) Resolve(ctx context.Context, code string) (store.Link, error) {
	if !shortener.ValidCode(code) {
		return store.Link{}, store.ErrNotFound
	}
	return h.store.GetLinkByCode(ctx, nil, code)
}

// List returns up to pageSize links ordered newest-first, plus a cursor
// for the next page (0 when there are no more rows). beforeID, when
// non-zero, advances past a previous page; pass 0 for the first page.
//
// Internally it requests pageSize+1 rows so the caller can detect "more
// available" without a separate COUNT query.
func (h *Links) List(ctx context.Context, pageSize int, beforeID int64) ([]store.Link, int64, error) {
	if pageSize <= 0 {
		return nil, 0, nil
	}
	rows, err := h.store.ListLinks(ctx, nil, pageSize+1, beforeID)
	if err != nil {
		return nil, 0, err
	}
	if len(rows) <= pageSize {
		return rows, 0, nil
	}
	// Trim the probe row and use the last *kept* row's id as the cursor.
	rows = rows[:pageSize]
	return rows, rows[len(rows)-1].ID, nil
}

// Response renders a Link as the public LinkResponse shape (with the
// canonical short_url derived from BaseURL). Exported so the web handler
// can format recent-list entries the same way the API does.
func (h *Links) Response(l store.Link) LinkResponse { return h.makeResp(l) }

// --- handlers ---------------------------------------------------------------

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

// createWithRandomCode generates a fresh code and retries on the rare
// unique-collision. After h.retries failed attempts it gives up: that
// implies either an exhausted keyspace or a degenerate generator and
// should surface as a 500.
func (h *Links) createWithRandomCode(ctx context.Context, target string, expiresAt *time.Time) (store.Link, error) {
	for i := 0; i < h.retries; i++ {
		code, err := h.gen.Generate()
		if err != nil {
			return store.Link{}, fmt.Errorf("generate code: %w", err)
		}
		l, err := h.store.CreateLink(ctx, nil, code, target, expiresAt)
		if errors.Is(err, store.ErrCodeTaken) {
			h.logger.Warn("links: code collision; retrying", "attempt", i+1, "code", code)
			continue
		}
		return l, err
	}
	return store.Link{}, fmt.Errorf("failed to generate unique code after %d attempts", h.retries)
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
	if link.IsExpired() {
		return c.JSON(http.StatusGone, errResp(ErrCodeLinkExpired, "link has expired"))
	}
	return c.JSON(http.StatusOK, h.makeResp(link))
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
		return echo.NewHTTPError(http.StatusNotFound, "not found")
	}
	ctx := c.Request().Context()

	if target, ok := h.cacheGet(ctx, code); ok {
		h.recordClick(code)
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
	if link.IsExpired() {
		return echo.NewHTTPError(http.StatusGone, "link has expired")
	}

	h.cachePut(ctx, link)
	h.recordClick(link.Code)
	return c.Redirect(http.StatusFound, link.TargetURL)
}

// recordClick increments the link's click counter on a detached
// goroutine so a slow UPDATE -- or an outright DB outage -- never
// delays the 302 the user is waiting for. The detached context has a
// short timeout so a permanently stuck DB doesn't leak goroutines
// without bound. The WaitGroup makes it possible for tests (and a
// future graceful-shutdown path) to wait for in-flight clicks; in
// production we don't otherwise observe it.
//
// Transient DB errors (deadlock, serialization failure, statement
// completion unknown) are retried with exponential backoff + jitter
// inside the goroutine; non-transient errors and exhausted budgets
// are logged and dropped, since the click counter is best-effort.
func (h *Links) recordClick(code string) {
	h.bgWG.Add(1)
	go func() {
		defer h.bgWG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), clickTimeout)
		defer cancel()
		if err := h.incrementClicksWithRetry(ctx, code); err != nil {
			h.logger.Warn("links: increment clicks failed", "error", err, "code", code)
		}
	}()
}

// incrementClicksWithRetry calls store.IncrementClicks with bounded
// exponential backoff (jittered) on transient errors. It returns the
// last error observed (or nil on success). The function is unexported
// because the retry policy is bespoke to this call site -- the
// counter is idempotent under repeated UPDATE, so re-issuing an
// ambiguously-completed statement is safe.
func (h *Links) incrementClicksWithRetry(ctx context.Context, code string) error {
	var err error
	backoff := clickRetryBaseDelay
	for attempt := 1; attempt <= clickRetryAttempts; attempt++ {
		err = h.store.IncrementClicks(ctx, nil, code)
		if err == nil {
			return nil
		}
		if !store.IsTransient(err) {
			return err
		}
		if attempt == clickRetryAttempts {
			break
		}
		// Sleep before the next attempt; bail early if the caller's
		// context (clickTimeout) expires while we wait. Returning the
		// most recent transient err -- not ctx.Err() -- gives the log
		// line a more useful root-cause for an operator scanning for
		// 40P01 spikes.
		select {
		case <-ctx.Done():
			return err
		case <-time.After(jitter(backoff)):
		}
		backoff *= 2
		if backoff > clickRetryMaxBackoff {
			backoff = clickRetryMaxBackoff
		}
	}
	return err
}

// jitter returns d randomized within [d/2, 3d/2). Decorrelated jitter
// would smear retries even more uniformly, but for a single-instance
// best-effort counter the simpler full-range jitter is plenty.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	half := d / 2
	// rand.Int64N panics on n<=0; the d>0 guard above keeps us safe.
	// math/rand/v2 is the right tool here -- the jitter only needs to
	// decorrelate retries across goroutines, not resist prediction.
	return half + time.Duration(rand.Int64N(int64(d))) //nolint:gosec // non-cryptographic jitter
}

// WaitForBackgroundTasks blocks until every recordClick goroutine
// started so far has finished, or until d elapses. Intended for tests
// that need to assert the click counter advanced. Returns true when
// every goroutine completed in time.
func (h *Links) WaitForBackgroundTasks(d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		h.bgWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
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

// cachePut stores the link's target under its short-code key with a
// TTL clamped to the link's remaining lifetime. The clamp guarantees
// the cache never serves an expired redirect, so the Redirect hot
// path doesn't have to re-check expiry on every cache hit.
//
// A non-positive remaining lifetime (already expired, or expiring
// within a second) skips the Set entirely: caching a value that is
// already past its deadline buys nothing and just wastes Redis ops.
func (h *Links) cachePut(ctx context.Context, l store.Link) {
	ttl := h.cacheTTL
	if l.ExpiresAt != nil {
		remaining := time.Until(*l.ExpiresAt)
		if remaining <= time.Second {
			return
		}
		if remaining < ttl {
			ttl = remaining
		}
	}
	if err := h.cache.Set(ctx, cacheKey(l.Code), l.TargetURL, ttl); err != nil {
		h.logger.Warn("links: cache set failed", "error", err, "code", l.Code)
	}
}

func cacheKey(code string) string { return "link:" + code }

// --- helpers ----------------------------------------------------------------

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

// validateExpiresAt enforces that an explicit expiry is in the future
// (with a small grace window for honest clock skew between the client
// and server). A nil pointer means "never expires" and is always OK.
func validateExpiresAt(t *time.Time) error {
	if t == nil {
		return nil
	}
	if !t.After(time.Now().Add(-clockSkewGrace)) {
		return errors.New("expires_at must be in the future")
	}
	return nil
}

// normalizeURL returns a canonical form of target suitable for dedup
// lookups: lowercase scheme + host, default port (:80/:443) stripped,
// and a lone "/" path removed. Conservative on purpose -- query and
// fragment are left intact since they're user-meaningful, and
// percent-encoding case (%2A vs %2a, RFC-equivalent) is not touched
// because changing it could change semantics for servers that mis-decode.
// Trailing-dot hostnames (`example.com.`) are also left alone for the
// same reason; in practice no real client emits them.
//
// Returns an error for inputs that would not pass validateTargetURL; in
// practice callers should validate first, but this function is defensive.
func normalizeURL(target string) (string, error) {
	u, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)
	// Strip the default port. The leading ":" anchors the suffix match,
	// so something like ":8080" cannot accidentally be stripped: the
	// last 3 bytes of "...:8080" are "080", not ":80".
	switch {
	case u.Scheme == "http" && strings.HasSuffix(host, ":80"):
		host = strings.TrimSuffix(host, ":80")
	case u.Scheme == "https" && strings.HasSuffix(host, ":443"):
		host = strings.TrimSuffix(host, ":443")
	}
	u.Host = host
	// A bare "/" path is equivalent to no path; drop it for stable dedup.
	if u.Path == "/" {
		u.Path = ""
	}
	return u.String(), nil
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
