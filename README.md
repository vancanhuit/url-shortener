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
just init             # install husky/commitlint dev dependencies
just build            # build ./bin/url-shortener
just test             # run unit tests with -race -v -cover
just test-integration # bring up infra, migrate, run -tags=integration tests
just lint             # run golangci-lint (auto-installs the pinned version)
just up               # bring up infra (db + redis) for native iteration
just dev              # bring up the full Docker stack (db + redis + server)
just down             # tear it down (also removes volumes)
```

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
| `URL_SHORTENER_REDIS_URL`      | _(empty)_                     | Redis connection string. Redacted when printed.     |
| `URL_SHORTENER_AUTO_MIGRATE`   | `false`                       | When `true`, `run` applies migrations before serving. Convenient for local dev / single-replica CI; production deployments should leave this off and run `migrate up` as a separate step. |

Run `url-shortener config` to print the fully resolved configuration with
passwords replaced by `REDACTED`.

## Operational endpoints

The HTTP server exposes three operational endpoints:

| Endpoint    | Purpose                          | Behaviour                                                                                     |
| ----------- | -------------------------------- | --------------------------------------------------------------------------------------------- |
| `/healthz`  | Liveness probe                   | Always returns `200` + `{"status":"ok"}` while the process is responsive. No dependencies.    |
| `/readyz`   | Readiness probe                  | Pings every registered dependency. Returns `200` when all are healthy, `503` otherwise.       |
| `/version`  | Build metadata                   | Returns `{"version":"...","commit":"...","date":"..."}` baked into the binary at build time.  |

`/readyz` checks Postgres (when `URL_SHORTENER_DATABASE_URL` is set) and
Redis (when `URL_SHORTENER_REDIS_URL` is set). Each check has its own line
in the JSON body so operators can see which dependency is unhappy.

## Layout (target)

Directories marked _(present)_ already exist on `main`; the rest are added in
upcoming phases of the rewrite plan.

```
cmd/url-shortener/        binary entry point                            (present)
internal/
  cli/                    cobra commands (run, migrate, version, config) (present)
  config/                 viper-based env config loader                   (present)
  buildinfo/              version / commit / date set via -ldflags        (present)
  server/                 echo setup, middleware, lifecycle              (present)
  handlers/               http handlers (operational; json api + html added later) (present)
  shortener/              short-code generation
  store/                  pgx-based repository                           (present)
  cache/                  redis client wrapper                           (present)
  migrate/                goose runner over embedded SQL                 (present)
migrations/               goose .sql migrations (//go:embed)             (present)
web/templates/            html/template files
web/static/               static assets (incl. compiled tailwind css)
web/tailwind/             tailwind v4 toolchain (npm)
.dagger/                  dagger module (Go SDK)
```

## License

To be added.
