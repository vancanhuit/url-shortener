# ADR 0004: Dedup auto-generated codes via a partial unique index

- Status: Accepted
- Date: 2025-11-15

## Context

When a caller submits `POST /api/v1/links` without a custom code,
the service generates one. We want repeated submissions of the
same `target_url` to converge on a single short code rather than
mint a fresh one every time — otherwise the table grows linearly
with traffic for trivially duplicated inputs (newsletter
auto-fillers, monitoring probes, marketing redirects that get
re-imported).

The dedup approaches considered were:

- **SELECT-then-INSERT (`if row exists return it else insert`).**
  Vulnerable to a TOCTOU race: two concurrent callers both observe
  no row, both insert, and one wins on a unique-violation while
  the other silently creates a second row (or both create rows if
  there is no unique index on `target_url`).
- **Application-level mutex.** Solves the race within one process
  but not across replicas, and serialises every create-link call
  on a single goroutine.
- **Advisory lock keyed on `hash(target_url)`.** Cross-replica
  safe, but adds an extra round-trip per insert and ties dedup
  correctness to lock-acquisition correctness.
- **`INSERT ... ON CONFLICT DO UPDATE`** against a unique index on
  `(target_url) WHERE expires_at IS NULL AND deleted_at IS NULL`.
  Postgres serialises the contention at the index level; the
  conflict path can `RETURNING` the existing row so callers always
  get back the canonical code in one round-trip.

The partial index is important: an expired or soft-deleted row
for the same `target_url` must not block a fresh permanent insert.
A full unique index on `target_url` would make resurrection of an
old URL impossible without a hard delete first.

## Decision

- Add a partial unique index on `links(target_url)` covering only
  auto-generated, live rows:
  `WHERE auto_dedup = true AND expires_at IS NULL AND deleted_at IS NULL`.
  Custom user-supplied codes set `auto_dedup = false` and therefore
  do not participate in the dedup index, so two users picking the
  same `target_url` with different custom codes both succeed.
- Implement `CreateAutoLink` as a single
  `INSERT ... ON CONFLICT (target_url) WHERE ... DO UPDATE SET code = links.code RETURNING *`.
  The `DO UPDATE` is a deliberate no-op against `links.code` so that
  `RETURNING` populates the row even on the conflict path (a plain
  `DO NOTHING` would return zero rows for losers, forcing a second
  `SELECT`).
- Detect winner-vs-loser in Go by comparing the returned code
  against the candidate code the caller supplied: equal means we
  inserted (winner), different means we collided onto an
  existing row (loser).
- Cover the race with an integration test that spawns several
  goroutines concurrently calling `CreateAutoLink` on the same
  target and asserts exactly one winner.

## Consequences

Positive:
- Atomic: no application-level locking, no second round-trip,
  no risk of a missed race.
- Cross-replica safe by construction (Postgres serialises the
  conflict).
- Expired / soft-deleted rows do not block resurrection of the
  same URL as a fresh permanent link.

Negative:
- The "code returned == code I proposed" winner test is a subtle
  invariant: it relies on candidate codes being unique-per-call.
  The code generator collision-rate is low enough that this holds
  in practice; the integration test pins the behaviour.
- The `DO UPDATE SET code = links.code` no-op is a Postgres-specific
  idiom (chosen so `RETURNING` populates on the conflict path).
  Reviewers occasionally flag the no-op as redundant. It is not —
  removing it forces a second `SELECT` for losers.

Neutral:
- Custom (user-supplied) codes go through `CreateLink` with
  `auto_dedup = false`, which uses the unique index on `code` and
  surfaces `ErrCodeTaken` on conflict rather than dedupe-merging.
  Auto-generated and user-specified codes intentionally have
  different "what happens on conflict" semantics; the
  `auto_dedup` flag is what keeps the two paths from
  interfering through the shared `target_url` index.
