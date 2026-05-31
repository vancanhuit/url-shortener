# ADR 0002: Use sqlc for data-access code generation

- Status: Accepted
- Date: 2025-11-15

## Context

The service has a small, append-mostly schema (one `links` table)
with a handful of query shapes: insert, lookup-by-code,
lookup-by-target, list with pagination, increment click counter,
soft delete, hard purge. The query set is unlikely to grow into
the dozens.

The data-access options considered were:

- **Hand-written `database/sql` with prepared statements.**
  Type-safe at the function-signature level, but the row-scan
  boilerplate (`rows.Scan(&l.ID, &l.Code, &l.TargetURL, ...)`) is
  easy to misalign with the schema and only fails at runtime.
- **An ORM (`gorm`, `bun`, `ent`).** Solves the boilerplate but
  takes ownership of the query language: ad-hoc Postgres features
  we already use (partial unique indexes for dedup, `make_interval`
  for purge windows, advisory locks for migrations) require either
  raw-SQL escape hatches or upstream support. Schema migrations
  are duplicated between the ORM model and the SQL files.
- **`sqlc`.** Queries are written in plain SQL in
  `internal/store/queries/`; `sqlc generate` produces typed Go
  functions in `internal/store/db/`. The generator validates each
  query against the live schema (driven from `migrations/`), so
  a typo in a column name fails at code-gen time, not at runtime.

## Decision

Use [`sqlc`](https://sqlc.dev) as the source of truth for all
query-shape Go code. Hand-write only the thin `Store` wrapper in
`internal/store/postgres.go`, which:

- adds the `DBTX` parameter so callers can pass either the pool or
  a `pgx.Tx`,
- maps generated `db.Link` rows to the public `store.Link`
  pointer-time-fields shape,
- and classifies pgx errors into the public `Err*` sentinels.

Generated code lives at `internal/store/db/` and is committed to
the repository (so consumers do not need `sqlc` installed to build).

## Consequences

Positive:
- Adding or modifying a query is "edit `.sql` file, run
  `sqlc generate`" — no manual Go updates, no risk of scan
  argument drift.
- Hand-written wrapper layer keeps public types decoupled from
  generated ones; we can change pgx versions or even the SQL
  driver without breaking handlers.

Negative:
- Generated code is in the repo, so PRs touching SQL include the
  generated diff. Reviewers must remember to run `sqlc generate`
  locally rather than hand-editing `internal/store/db/`.
- One more developer tool (`sqlc`) in `mise.toml`.

Neutral:
- `sqlc` does not abstract the underlying driver, so we keep
  using `pgx` directly for transactions, advisory locks, and
  pool tuning. That matches the existing
  [ADR 0001](0001-chi-router.md) philosophy of staying close to
  the stdlib / driver surface.
