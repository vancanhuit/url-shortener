package handlers

// Metrics records business-level counters for the links handler: how many
// links are shortened (and whether they were freshly created or served
// from the dedup path), how redirects resolve, and how often the
// auto-generated-code loop collides.
//
// It is deliberately a small interface rather than a direct Prometheus
// dependency so the handlers package stays free of metrics-library imports
// and tests can run against the no-op default. Every method must be safe
// for concurrent use; the server wires a Prometheus-backed implementation.
type Metrics interface {
	// IncShorten records a successful create. outcome distinguishes a
	// freshly inserted row from one reused via the dedup path.
	IncShorten(outcome ShortenOutcome)
	// IncRedirect records how a /r/{code} lookup resolved.
	IncRedirect(outcome RedirectOutcome)
	// IncCodeCollision records one auto-generated-code collision (a
	// retry inside the create loop). A nonzero rate is normal as the
	// keyspace fills; a spike signals an undersized code length.
	IncCodeCollision()
}

// ShortenOutcome labels a successful shorten by how the row was obtained.
type ShortenOutcome string

const (
	// ShortenCreated is a brand-new row inserted for this request.
	ShortenCreated ShortenOutcome = "created"
	// ShortenDeduped is an existing permanent row reused for the same
	// normalized target (auto-generated, non-expiring requests only).
	ShortenDeduped ShortenOutcome = "deduped"
)

// RedirectOutcome labels how a redirect lookup terminated.
type RedirectOutcome string

const (
	// RedirectCacheHit served a 302 straight from a positive cache entry.
	RedirectCacheHit RedirectOutcome = "cache_hit"
	// RedirectNegativeHit short-circuited on a negative cache entry (404).
	RedirectNegativeHit RedirectOutcome = "negative_hit"
	// RedirectStoreHit resolved from the store after a cache miss (302).
	RedirectStoreHit RedirectOutcome = "store_hit"
	// RedirectNotFound found no live row (404).
	RedirectNotFound RedirectOutcome = "not_found"
	// RedirectGone matched a soft-deleted or expired row (410).
	RedirectGone RedirectOutcome = "gone"
	// RedirectError aborted on an internal lookup error (500).
	RedirectError RedirectOutcome = "error"
)

// noopMetrics is the default Metrics implementation: it discards every
// observation. Used when the handler is constructed without an explicit
// Metrics (all current tests, and any embedding that doesn't care).
type noopMetrics struct{}

func (noopMetrics) IncShorten(ShortenOutcome)   {}
func (noopMetrics) IncRedirect(RedirectOutcome) {}
func (noopMetrics) IncCodeCollision()           {}
