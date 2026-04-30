# url-shortener

A small URL shortener service written in Go.

> **Status:** active rewrite from Python/FastAPI to Go. The repository history
> was reset on `main` to start fresh; see `CONTRIBUTING.md` for the workflow.

## Tech stack

- Go 1.26 with the standard library's `log/slog`, `net/http`, etc.
- [Echo v5](https://echo.labstack.com/) for HTTP routing and middleware.
- [Cobra](https://cobra.dev/) + [Viper](https://github.com/spf13/viper) for CLI
  and 12-factor configuration.
- PostgreSQL via [`pgx/v5`](https://github.com/jackc/pgx).
- Redis via [`go-redis/v9`](https://github.com/redis/go-redis).
- [Goose](https://github.com/pressly/goose) for database migrations
  (embedded in the binary).
- HTML UI with [Tailwind CSS v4](https://tailwindcss.com/) and
  [HTMX 2.x](https://htmx.org/).
- [Just](https://github.com/casey/just) as the task runner.
- [Dagger](https://dagger.io/) for the CI/CD pipeline (added in a later phase).

## Getting started

Prerequisites: Go 1.26+, Node.js 24+, Just, `golangci-lint` v2, Docker
(for the local stack).

```sh
just init                              # install husky/commitlint dev dependencies
just web-install                       # install npm deps for the tailwind + htmx toolchain
just web-build                         # compile tailwind CSS and vendor htmx into web/static
just web-watch                         # tailwind in watch mode for UI iteration
just build                             # build ./bin/url-shortener (depends on web-build)
just test                              # run unit tests with -race -v -cover
just test-integration                  # bring up test-profile infra, migrate, run -tags=integration tests
just lint                              # run golangci-lint (auto-installs the pinned version)
docker compose up --wait -d            # bring up the full local dev stack (db + redis + server on 5432/6379/8080)
docker compose down -v                 # tear down the dev stack
docker compose --profile=test down -v  # tear down the test-profile stack (db-test + redis-test on 5433/6380)
```

The `compose.yaml` defines two stacks side by side: the **default** services
(`db`, `redis`, `server`) for local dev on standard ports, and a **`test`
profile** (`db-test`, `redis-test`) on alternate ports (5433, 6380) with
their own volumes. Running the integration suite while a dev stack is up
is therefore safe; the two never collide.

The HTML UI is embedded in the binary via `//go:embed`, so the compiled
assets in `web/static/` must exist at `go build` time. They're treated as
build artifacts (gitignored): `just build` always runs `just web-build`
first so a fresh checkout works without ceremony, and the multi-stage
`Dockerfile` has a dedicated `node` stage that produces them before the
Go builder runs.

## Usage

```sh
./bin/url-shortener --help        # list all subcommands
./bin/url-shortener version       # print version / commit / build date
./bin/url-shortener version --json
./bin/url-shortener config        # print resolved config (secrets redacted)
./bin/url-shortener run           # start the HTTP server (graceful shutdown on SIGINT/SIGTERM)
./bin/url-shortener migrate up    # apply pending database migrations
./bin/url-shortener migrate down  # roll back the most recent migration
./bin/url-shortener migrate status
./bin/url-shortener migrate up --database-url postgres://...  # override URL_SHORTENER_DATABASE_URL
./bin/url-shortener healthcheck   # probe /healthz; used by the docker HEALTHCHECK
```

## Configuration

All configuration comes from environment variables prefixed with
`URL_SHORTENER_` (12-factor style). Defaults are tuned for production; the
local `compose.yaml` overrides them for development.

| Variable                       | Default                       | Description                                         |
| ------------------------------ | ----------------------------- | --------------------------------------------------- |
| `URL_SHORTENER_ENV`            | `prod`                        | `dev` or `prod`. Influences log-format default.     |
| `URL_SHORTENER_ADDR`           | `:8080`                       | TCP listen address.                                 |
| `URL_SHORTENER_BASE_URL`       | `http://localhost:8080`       | Public origin used when building short-link URLs.   |
| `URL_SHORTENER_LOG_LEVEL`      | `info`                        | One of `debug`, `info`, `warn`, `error`.            |
| `URL_SHORTENER_LOG_FORMAT`     | `text` in dev, `json` in prod | `text` (human-readable) or `json` (structured).     |
| `URL_SHORTENER_DATABASE_URL`   | _(empty)_                     | Postgres connection string. Redacted when printed.  |
| `URL_SHORTENER_REDIS_URL`      | _(empty)_                     | **Required.** Redis connection string. Redacted when printed.   |
| `URL_SHORTENER_AUTO_MIGRATE`   | `false`                       | When `true`, `run` applies migrations before serving. Convenient for local dev / single-replica CI; production deployments should leave this off and run `migrate up` as a separate step. |
| `URL_SHORTENER_CODE_LENGTH`    | `7`                           | Length of auto-generated short codes (base62). Must be in [4, 64]. |

Run `url-shortener config` to print the fully resolved configuration with
passwords replaced by `REDACTED`.

## API

```http
POST /api/v1/links
Content-Type: application/json

{"target_url": "https://example.com/...", "code": "optional"}
```

Response `201 Created` for a freshly minted code, `200 OK` when an existing
row was reused (see Deduplication below):

```json
{
  "code": "a1B2c3D",
  "short_url": "https://your.host/r/a1B2c3D",
  "target_url": "https://example.com/...",
  "created_at": "2026-04-30T06:48:00Z"
}
```

| Endpoint                  | Purpose                                                              |
| ------------------------- | -------------------------------------------------------------------- |
| `POST /api/v1/links`      | Create a link. Auto-generates a base62 code, or accepts a user one.  |
| `GET  /api/v1/links/:code`| Fetch link metadata as JSON.                                         |
| `GET  /r/:code`           | 302 redirect to the link's `target_url`. Read-through Redis cache.   |

Validation: `target_url` must be `http`/`https`, have a host, and be at most
2048 characters. User-supplied codes must match `[0-9A-Za-z]{4,64}`. Status
codes: `400` for malformed JSON, `409` for a duplicate user-supplied code,
`422` for validation failures, `404` for unknown codes.

### Deduplication

Auto-generated codes are idempotent on the (normalized) target URL: a
second `POST` of the same destination returns the existing row with
`200 OK` instead of minting a new code. URLs are normalized before lookup
and storage:

- scheme and host are lowercased
- the default port is stripped (`:80` for `http`, `:443` for `https`)
- a bare `/` path is removed
- everything else (path case, query string, fragment, percent-encoding)
  is left untouched

User-supplied codes always create a new row, even when the target is
already present elsewhere -- two codes can legitimately point at the same
destination. Dedup is best-effort under concurrent writes (no unique
constraint on `target_url`).

## Web UI

The binary serves a small HTML UI at `/`:

- A paste-URL form with optional custom code, posted via HTMX so success
  and error states swap inline without a page reload.
- A copy-to-clipboard button on the result panel.
- A "Recent" list backed by Postgres, paginated cursor-style via the
  `id DESC` order. The Load more button fetches `/recent?before=<id>`
  and HTMX appends rows + replaces the cursor.

Static assets are served under `/static/` from the embedded FS:

- `/static/styles.css` &mdash; compiled Tailwind v4 bundle
- `/static/htmx.min.js` &mdash; vendored HTMX 2

## Operational endpoints

The HTTP server exposes three operational endpoints:

| Endpoint    | Purpose                          | Behaviour                                                                                     |
| ----------- | -------------------------------- | --------------------------------------------------------------------------------------------- |
| `/healthz`  | Liveness probe                   | Always returns `200` + `{"status":"ok"}` while the process is responsive. No dependencies.    |
| `/readyz`   | Readiness probe                  | Pings every registered dependency. Returns `200` when all are healthy, `503` otherwise.       |
| `/version`  | Build metadata                   | Returns `{"version":"...","commit":"...","date":"..."}` baked into the binary at build time.  |

`/readyz` checks Postgres (when `URL_SHORTENER_DATABASE_URL` is set) and
Redis (always required). Each check has its own line in the JSON body so
operators can see which dependency is unhappy.

## Layout (target)

Directories marked _(present)_ already exist on `main`; the rest are added in
upcoming phases of the rewrite plan.

```
cmd/url-shortener/        binary entry point                            (present)
internal/
  cli/                    cobra commands (run, migrate, version, config, healthcheck) (present)
  config/                 viper-based env config loader                   (present)
  buildinfo/              version / commit / date set via -ldflags        (present)
  server/                 echo setup, middleware, lifecycle              (present)
  handlers/               operational, json links api, html web ui       (present)
  shortener/              short-code generation                          (present)
  store/                  pgx-based repository                           (present)
  cache/                  redis client wrapper                           (present)
  migrate/                goose runner over embedded SQL                 (present)
migrations/               goose .sql migrations (//go:embed)             (present)
web/                      html/template files + embed                   (present)
web/templates/            html/template files                            (present)
web/static/               compiled tailwind css + vendored htmx          (present)
web/tailwind/             tailwind v4 toolchain (npm)                    (present)
.dagger/                  dagger module (Go SDK)
```

## License

To be added.
