# ADR 0001: Use go-chi for HTTP routing

- Status: Accepted
- Date: 2025-11-15

## Context

The HTTP layer needs a router that:

- supports `net/http` middleware composition (so we can keep
  `slog`-based request logging, Prometheus RED metrics, security
  headers, body-size caps, and CORS as standalone middlewares);
- exposes the matched route pattern so RED metrics group by
  template (`/r/{code}`) rather than by URL (`/r/abc123`,
  `/r/def456`, …) and produce bounded label cardinality;
- handles `*http.Request`/`http.Handler` types directly so it
  composes with `oapi-codegen`'s strict-mode handlers and with
  `httptest` in unit tests; and
- has no runtime dependency on any framework-style "context"
  type (request-scoped values stay in `context.Context`).

The candidates considered were:

- **`net/http.ServeMux` (Go 1.22+).** Method+path matching landed
  in 1.22, but there is no way to recover the matched route
  template from inside a middleware, which makes per-template
  metrics impossible without per-route registration boilerplate.
- **`labstack/echo`.** Framework-style: handlers take an
  `echo.Context`, middlewares are echo-specific, and integrating
  third-party `http.Handler` middleware requires adapter shims.
- **`gin-gonic/gin`.** Same framework-style objection plus a
  reflection-heavy binding system we do not need.
- **`go-chi/chi`.** Idiomatic `http.Handler` + `http.HandlerFunc`,
  middleware uses the stdlib signature, exposes the matched
  pattern via `chi.RouteContext(ctx).RoutePattern()`.

## Decision

Use `github.com/go-chi/chi/v5` as the router for all HTTP routes
mounted by `internal/server`. Continue to write middleware as plain
`func(http.Handler) http.Handler`.

## Consequences

Positive:
- Prometheus middleware can label metrics by `chi`'s route pattern.
- Handler signatures stay stdlib-compatible, so `oapi-codegen`'s
  strict-mode adapters and `httptest` work without shims.
- Switching to a different router later is a localised change:
  call sites are `r.Get`/`r.Post`/`r.Group`, not framework-specific.

Negative:
- `chi` is one more direct dependency. Mitigated by it being a
  small, stable library with no transitive bloat.

Neutral:
- We do not use `chi`'s middleware-as-method pattern; everything
  is plain `func(http.Handler) http.Handler` so swapping it is
  mechanical.
