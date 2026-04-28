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

Prerequisites: Go 1.26+, Node.js 20+, Just, `golangci-lint` v2, Docker
(for the local stack).

```sh
just init        # install husky/commitlint dev dependencies
just build       # build ./bin/url-shortener
just test        # run unit tests
just lint        # run golangci-lint
```

The CLI surface, HTTP server, database integration, and CI pipeline are added
in subsequent phases of the rewrite plan; this commit is the project scaffold.

## Layout (target)

```
cmd/url-shortener/        binary entry point
internal/
  cli/                    cobra commands (run, migrate, version, config)
  config/                 viper-based env config loader
  buildinfo/              version / commit / date set via -ldflags
  server/                 echo setup, middleware, lifecycle
  handlers/               http handlers (json api + html + health)
  shortener/              short-code generation
  store/                  pgx-based repository
  cache/                  redis client wrapper
  migrate/                goose runner over embedded SQL
migrations/               goose .sql migrations (//go:embed)
web/templates/            html/template files
web/static/               static assets (incl. compiled tailwind css)
web/tailwind/             tailwind v4 toolchain (npm)
.dagger/                  dagger module (Go SDK)
```

## License

To be added.
