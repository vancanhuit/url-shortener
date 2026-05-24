package handlers

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/vancanhuit/url-shortener/internal/store"
)

// --- cache helpers ----------------------------------------------------------
//
// All cache failures are logged and swallowed: the cache is on the hot path
// for redirects but it is an optimisation, not a source of truth, so a
// transient outage degrades to a store-only round-trip rather than 5xx.

// cacheGet returns the cached redirect target for code. The negative
// flag is true when the cache holds a sentinel marking the code as
// known-unresolvable (404 / 410); callers should short-circuit and
// return 404 in that case without touching the store.
func (h *Links) cacheGet(ctx context.Context, code string) (target string, hit bool, negative bool) {
	v, ok, err := h.cache.Get(ctx, cacheKey(code))
	if err != nil {
		h.logger.Warn("links: cache get failed", "error", err, "code", code)
		return "", false, false
	}
	if !ok {
		return "", false, false
	}
	if v == cacheNegativeSentinel {
		return "", true, true
	}
	return v, true, false
}

// cachePutNegative records that code does not resolve, so subsequent
// requests in the next NegativeCacheTTL window can be answered without
// a Postgres lookup. Failures are logged and ignored: the negative
// cache is a defense-in-depth optimization, never a correctness gate.
func (h *Links) cachePutNegative(ctx context.Context, code string) {
	if err := h.cache.Set(ctx, cacheKey(code), cacheNegativeSentinel, h.negCacheTTL); err != nil {
		h.logger.Warn("links: negative cache set failed", "error", err, "code", code)
	}
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

// --- background click-counter machinery -------------------------------------

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
	h.bgWG.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), clickTimeout)
		defer cancel()
		if err := h.incrementClicksWithRetry(ctx, code); err != nil {
			h.logger.Warn("links: increment clicks failed", "error", err, "code", code)
		}
	})
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
