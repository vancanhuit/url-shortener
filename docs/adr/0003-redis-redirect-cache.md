# ADR 0003: Cache redirect lookups in Redis

- Status: Accepted
- Date: 2025-11-15

## Context

`GET /r/{code}` is the hot path of the service: every short link
follower hits it, and the only work it has to do is read one row
from `links` and issue a 302. Postgres can serve that load, but:

- the read amplifies the connection-pool pressure for every other
  query the service runs (create-link, list, soft-delete), and
- when a code does not exist or is expired/deleted, we still pay a
  full round-trip to learn that — and "not found" floods (scanners,
  stale links, typos in QR codes) can be a meaningful share of
  traffic.

The options for shaving the read off the hot path were:

- **In-process LRU only.** Zero network hop, but cache state is
  per-replica: a redirect that was warmed on replica A is cold on
  replica B, and revoking a link (soft delete) requires a custom
  fan-out (or a process restart) to invalidate every replica.
- **Postgres `LISTEN/NOTIFY` for in-process invalidation.**
  Removes the fan-out problem above, but adds a long-lived
  connection per replica and is awkward to plumb through `pgx`'s
  pool.
- **Redis as a shared cache.** One round-trip per lookup
  regardless of replica, simple TTL semantics, and revocation is
  just a `DEL` on the canonical key.

We chose Redis primarily for the shared-state property and for the
ease of negative caching (so "not found" replies don't repeatedly
hit Postgres).

## Decision

Use Redis as a shared lookup cache for `GET /r/{code}`. Cache both
positive answers (the resolved target URL) and negative answers
(a sentinel value for not-found / gone), each with their own TTL:

- `URL_SHORTENER_CACHE_TTL` (default 1 hour) for positives,
- `URL_SHORTENER_NEGATIVE_CACHE_TTL` (default 30 seconds) for
  negatives — short enough that a typo'd code becoming valid
  after the operator creates it propagates quickly.

Cache invalidation on soft-delete / hard-purge is the responsibility
of the write-side handler: it issues a `DEL` after the SQL write
commits. Redis is a hard dependency at startup (`config.Validate()`
fails fast on an empty or malformed `URL_SHORTENER_REDIS_URL`); the
service has no in-process fallback because the operational complexity
of running with a partially-populated cache outweighs the convenience.

## Consequences

Positive:
- Cold Postgres replica failovers do not cause a redirect-traffic
  thundering herd: Redis still answers most lookups.
- Negative caching absorbs `404`-spam (scanners, QR misreads, old
  links) at single-digit-microsecond latency.
- Cache is shared across replicas, so horizontal scale-out does
  not divide the warm-cache benefit.

Negative:
- One more required dependency at deploy time.
- Cache TTL upper-bounds how stale a revoked link can be served
  on the read path between revocation and the next miss
  (mitigated by the write-side `DEL`, but it is best-effort: a
  failed `DEL` is logged, not retried).
- A Redis outage takes the redirect path down with it (by design;
  see the "no in-process fallback" note above). Treat it as the
  same SLO tier as Postgres.

Neutral:
- The cache layer lives behind a small interface in
  `internal/cache`, so replacing Redis with a different key/value
  store later is a localised change.
