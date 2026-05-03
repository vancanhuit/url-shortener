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

### Development environment

The toolchain assumes a **Unix-like host** &mdash; Linux, macOS, or
**WSL2** on Windows. The `Justfile` recipes use bash with
`set -euo pipefail`, the npm scripts shell out to `mkdir`/`cp`,
and the integration suite drives a local Docker daemon. Native
Windows (cmd / PowerShell) is **not** supported; if you're on
Windows, develop inside WSL2.

Prerequisites:

- **Go 1.26+** &mdash; the language. The pinned toolchain version
  lives in `go.mod` (`toolchain go1.26.x`); newer is fine, older
  isn't.
- **Node.js 24+** &mdash; for the Tailwind v4 + HTMX toolchain in
  `web/tailwind/`. Required only when building the web assets
  locally; the `Dockerfile` brings its own Node stage.
- **[Just](https://github.com/casey/just)** &mdash; the task runner;
  every workflow in this README routes through it.
- **`golangci-lint` v2** &mdash; auto-installed by `just lint` at
  the version pinned in the `Justfile`, so a manual install is
  optional.
- **Docker** with the **Compose v2 plugin** and **Buildx** &mdash;
  Compose drives the local Postgres/Redis stack and the
  integration suite; Buildx produces the multi-arch images and
  attaches the SBOM + provenance attestations.
- **`git`** with at least the project's full tag history (`git
  fetch --tags`); the build embeds a `git describe` version string
  via `LDFLAGS`, and the changelog generator walks the tag graph.

Optional but useful:

- **`jq`** &mdash; for inspecting SBOMs and JSON API responses.
- **`psql`** / **`redis-cli`** &mdash; for poking at the local
  stack outside the test harness.

The repository is the **single source of truth for tool versions**:
`Justfile` pins `GOLANGCI_LINT_VERSION` and `TRIVY_VERSION`, the
`Dockerfile` pins Go and Node `ARG`s, and CI overrides via env vars
defined in `.github/workflows/ci.yaml`. There are no global
installs of any of these tools required &mdash; running a recipe
will install what it needs into `$(go env GOPATH)/bin` on demand.

### Workflow cheatsheet

```sh
just init                              # install husky/commitlint dev dependencies
just web-install                       # install npm deps for the tailwind + htmx toolchain
just web-build                         # compile tailwind CSS and vendor htmx into web/static
just web-watch                         # tailwind in watch mode for UI iteration
just build                             # build ./bin/url-shortener (depends on web-build)
just test                              # run unit tests with -race -v -cover
just test-integration                  # bring up test-profile infra, migrate, run -tags=integration tests
just lint                              # run golangci-lint (auto-installs the pinned version)
just govulncheck                       # run govulncheck against the latest Go vuln database
just trivy-image                              # build the Docker image and scan it with Trivy (HIGH/CRITICAL)
docker compose --profile=dev up --wait -d     # bring up the full local dev stack (db + redis + server on 5432/6379/8080)
just compose-smoke                            # smoke-check a running stack: operational endpoints + shorten/redirect cycle
docker compose --profile=dev down -v          # tear down the dev stack
docker compose --profile=test down -v         # tear down the test-profile stack (db-test + redis-test on 5433/6380)
```

The `compose.yaml` defines two stacks side by side: the **`dev` profile**
(`db`, `redis`, `server`) for local dev on standard ports, and the **`test`
profile** (`db-test`, `redis-test`) on alternate ports (5433, 6380) with
their own volumes. Both profiles must be selected explicitly with
`--profile=...` -- a bare `docker compose up` starts nothing. Running the
integration suite while a dev stack is up is therefore safe; the two
never collide. The CI `compose-smoke` job drives the `dev` profile end
to end on every PR, so anything that breaks `up --wait` locally also
breaks CI.

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
| `URL_SHORTENER_DATABASE_URL`   | _(empty)_                     | **Required.** Postgres connection string. Redacted when printed. |
| `URL_SHORTENER_REDIS_URL`      | _(empty)_                     | **Required.** Redis connection string. Redacted when printed.   |
| `URL_SHORTENER_AUTO_MIGRATE`   | `false`                       | When `true`, `run` applies migrations before serving. Convenient for local dev / single-replica CI; production deployments should leave this off and run `migrate up` as a separate step. |
| `URL_SHORTENER_CODE_LENGTH`    | `7`                           | Length of auto-generated short codes (base62). Must be in [4, 64]. |
| `URL_SHORTENER_DB_MAX_CONNS`           | _(pgx default: max(4, NumCPU))_ | Upper bound on simultaneous Postgres connections. Set above the default to absorb burst load without queueing requests on the pool. |
| `URL_SHORTENER_DB_MIN_CONNS`           | _(pgx default: 0)_              | Idle connections kept warm. Useful to amortize TLS/handshake cost on bursty workloads. |
| `URL_SHORTENER_DB_MAX_CONN_LIFETIME`   | _(pgx default: 1h)_             | Hard cap on a connection's age. Forces rotation through floating-IP failovers and clears DB-side connection-state drift. Accepts Go duration syntax (e.g. `30m`, `2h`). |
| `URL_SHORTENER_DB_MAX_CONN_IDLE_TIME`  | _(pgx default: 30m)_            | How long a connection may sit unused before being closed. |
| `URL_SHORTENER_DB_HEALTH_CHECK_PERIOD` | _(pgx default: 1m)_             | How often pgx scans the pool for stale connections. |

Pool tunables are zero by default, in which case pgx's own defaults apply.
Production deployments behind a fronting proxy (PgBouncer, RDS Proxy)
typically want `DB_MAX_CONNS` raised to match the pool's per-replica
backend cap and `DB_MAX_CONN_LIFETIME` lowered to a few minutes.

Run `url-shortener config` to print the fully resolved configuration with
passwords replaced by `REDACTED`.

## API

```http
POST /api/v1/links
Content-Type: application/json

{
  "target_url": "https://example.com/...",
  "code":       "optional",
  "expires_at": "2026-05-01T00:00:00Z"
}
```

Response `201 Created` for a freshly minted code, `200 OK` when an existing
row was reused (see Deduplication below):

```json
{
  "code":        "a1B2c3D",
  "short_url":   "https://your.host/r/a1B2c3D",
  "target_url":  "https://example.com/...",
  "created_at":  "2026-04-30T06:48:00Z",
  "click_count": 0,
  "expires_at":   "2026-05-01T00:00:00Z"
}
```

`expires_at` is omitted from the response for permanent links (instead
of rendered as `null`), so a one-key check distinguishes them. The
field is RFC3339 in both directions; absent or `null` on the request
means "never expires". `click_count` is the lifetime hit count for
the redirect endpoint, bumped fire-and-forget on each successful
`/r/:code` so it never delays the 302.

| Endpoint                  | Purpose                                                              |
| ------------------------- | -------------------------------------------------------------------- |
| `POST /api/v1/links`      | Create a link. Auto-generates a base62 code, or accepts a user one.  |
| `GET  /api/v1/links/:code`| Fetch link metadata as JSON.                                         |
| `GET  /r/:code`           | 302 redirect to the link's `target_url`. Read-through Redis cache.   |

Validation: `target_url` must be `http`/`https`, have a host, and be at most
2048 characters. User-supplied codes must match `[0-9A-Za-z]{4,64}`.
`expires_at`, when supplied, must be in the future (a 30s grace window
absorbs honest client/server clock skew). Status codes: `400` for
malformed JSON, `409` for a duplicate user-supplied code, `422` for
validation failures, `404` for unknown codes, `410 Gone` for an
expired code (returned by both `GET /api/v1/links/:code` and
`GET /r/:code` -- distinct from `404` so clients can tell a once-valid
code from one that never existed).

Error responses carry a stable `code` field alongside the
human-readable `error` so clients can branch on the failure without
parsing the message:

```json
{ "error": "code already in use", "code": "code_taken" }
```

| HTTP | `code`              | When                                                      |
| ---- | ------------------- | --------------------------------------------------------- |
| 400  | `invalid_json_body` | Request body is not parseable JSON.                       |
| 422  | `validation_failed` | Input failed a validation rule (URL, code, or expiry).    |
| 409  | `code_taken`        | The user-supplied short code is already in use.           |
| 404  | `not_found`         | The code does not exist (either malformed or unknown).    |
| 410  | `link_expired`      | The link existed but its `expires_at` has passed.         |
| 500  | `internal_error`    | Any other server-side failure; details are logged only.   |

The string values are part of the public API contract and will not
change once published; new codes may be added.

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

Dedup is also suppressed whenever expiry is involved on either side: a
request carrying `expires_at` always mints a fresh code (so an
ephemeral request never silently extends a permanent row's lifetime),
and the dedup lookup excludes rows that themselves have a non-null
`expires_at` (so a permanent request never reuses an expiring row).

## Web UI

The binary serves a small HTML UI at `/`:

- A paste-URL form with optional custom code and an Expires select
  (Never / 1h / 1d / 7d / 30d), posted via HTMX so success and error
  states swap inline without a page reload.
- A copy-to-clipboard button on the result panel, plus an inline
  expiry hint when one was set.
- A "Recent" list backed by Postgres, paginated cursor-style via the
  `id DESC` order. Each row carries a click-count badge and (when
  applicable) an `expires in Nh / Nd left` / `expired` badge.
  - Each row's badges self-poll `GET /links/:code/badges` every 5
    seconds via HTMX and swap themselves outerHTML, so click counts
    and expiry labels refresh live without disturbing siblings or
    rows that were appended via *Load more*.
  - The *Load more* button fetches `/recent?before=<id>` and HTMX
    appends rows + replaces the cursor.

Static assets are served under `/static/` from the embedded FS:

- `/static/styles.css` &mdash; compiled Tailwind v4 bundle
- `/static/htmx.min.js` &mdash; vendored HTMX 2
- `/static/copy.js` &mdash; copy-to-clipboard helper
- `/static/theme.js` &mdash; dark/light theme toggle

## Operational endpoints

The HTTP server exposes three operational endpoints:

| Endpoint    | Purpose                          | Behaviour                                                                                     |
| ----------- | -------------------------------- | --------------------------------------------------------------------------------------------- |
| `/healthz`  | Liveness probe                   | Always returns `200` + `{"status":"ok"}` while the process is responsive. No dependencies.    |
| `/readyz`   | Readiness probe                  | Pings every registered dependency. Returns `200` when all are healthy, `503` otherwise.       |
| `/version`  | Build metadata                   | Returns `{"version":"...","commit":"...","date":"..."}` baked into the binary at build time.  |

`/readyz` pings Postgres and Redis -- both are mandatory runtime
dependencies (`config.Validate` rejects an empty
`URL_SHORTENER_DATABASE_URL` or `URL_SHORTENER_REDIS_URL` at startup).
Each check has its own line in the JSON body so operators can see
which dependency is unhappy.

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

## Releases

### Tagged releases

Pushing a `v*` semver tag triggers `.github/workflows/release.yaml` and
publishes:

- **Container images** to `ghcr.io/vancanhuit/url-shortener` for
  linux/amd64 + linux/arm64. Image tags match the git tag verbatim:
  pushing `v1.2.3` publishes `:v1.2.3`, and stable (non-prerelease)
  tags also move `:latest`. Pre-releases like `v1.2.3-beta1`
  publish only `:v1.2.3-beta1` and don't move `:latest`.
- **Binary archives** `url-shortener_<version>_<os>_<arch>.tar.gz` for
  linux + darwin on both architectures, attached to the matching
  GitHub Release alongside both per-archive `<archive>.tar.gz.sha256`
  files and an aggregate `SHA256SUMS`. Verify a single archive with
  `sha256sum -c <archive>.tar.gz.sha256`, or all of them at once with
  `sha256sum -c SHA256SUMS`.

The Release body is generated by [git-cliff](https://git-cliff.org/)
from the conventional-commit history between the previous and current
semver tags, configured in [`cliff.toml`](cliff.toml). Preview locally
with `just changelog` (defaults to "since latest tag"); the rendered
markdown is written to `dist/CHANGELOG.md` (gitignored) and also
echoed to stdout. Install git-cliff once via the snippet at the top
of the recipe in the [`Justfile`](Justfile).

See `CONTRIBUTING.md` for the tag-and-push flow that produces them.

### Development builds

The CI workflow publishes a dev image on every push to `main` so you
can pull the bleeding edge without waiting for a release tag:

| Tag | When updated | Use case |
| --- | --- | --- |
| `:edge` | every push to `main` | floating dev pointer |
| `:main-<short_sha>` | every push to `main` | pin to a specific commit |

Each push to `main` also uploads a binaries artifact to the workflow
run for users who deploy the binary directly rather than the image:

- `binaries-main-<short_sha>` -- `url-shortener_<version>_<os>_<arch>.tar.gz`
  for `linux`/`darwin` x `amd64`/`arm64`, plus per-archive
  `.tar.gz.sha256` files and an aggregate `SHA256SUMS`. 30-day
  retention, indexed by the same short-sha as the matching `:main-<short_sha>`
  image so the two stay in lockstep.

Pull-request runs do **not** push to GHCR. Instead they upload two
artifacts to the workflow run that you can grab from the Actions UI:

- `binaries-pr-<N>` -- `url-shortener_<version>_<os>_<arch>.tar.gz`
  for all four platforms, plus per-archive `.tar.gz.sha256` files
  and an aggregate `SHA256SUMS`. 7-day retention.
- `oci-image-pr-<N>` -- a single multi-arch OCI tarball; load it with
  `docker load -i url-shortener-oci.tar`. 7-day retention.

The binary embeds a `git describe` version string of the form
`<latest_tag>-<N>-g<sha>` (or just `<sha>` when no tags exist yet),
so `url-shortener version` always identifies which commit produced
a given build, regardless of where it came from.

### Image attestations (SBOM + provenance)

Every image published to GHCR &mdash; tagged releases, the `:edge`
floating tag, and the per-commit `:main-<sha>` tags &mdash; ships
with two in-toto attestations stored next to the manifest:

- An **SPDX SBOM** listing every Go module, npm package, and
  OS-level component baked into the runtime image. Useful for
  answering "are we shipping &lt;vulnerable-dep&gt;?" without
  rebuilding from source.
- A **SLSA-style provenance** attestation in `max` mode that pins
  the image digest to the workflow run, the source repo, and the
  commit SHA that produced it.

Fetch them with `docker buildx`:

```sh
docker buildx imagetools inspect ghcr.io/vancanhuit/url-shortener:v1.2.3 \
    --format '{{ json .SBOM }}'        # or .Provenance

# Pipe the SBOM straight to a vulnerability scanner:
docker buildx imagetools inspect ghcr.io/vancanhuit/url-shortener:v1.2.3 \
    --format '{{ json .SBOM.SPDX }}' | trivy sbom -
```

Pull-request OCI tarballs (`oci-image-pr-<N>`) carry the same
attestations, so reviewers can run the same commands against a
loaded image.

## Branch protection

Branch- and tag-protection rules live in
[`.github/rulesets/`](.github/rulesets/) as native GitHub repository
rulesets, one JSON file per ruleset. The
[`sync-rulesets`](.github/workflows/sync-rulesets.yaml) workflow
walks every file in that directory and applies it via `gh api` on
every push that touches the JSON, so the files in the repo are the
single source of truth. Drift introduced through the GitHub UI is
reverted on the next sync run.

### `main.json` (target: branch)

Applies to the default branch (`~DEFAULT_BRANCH`) and enforces:

- **Pull-request only** &mdash; no direct pushes (review count is 0;
  the gate is the PR, not an approver)
- **Required CI checks** &mdash; `commitlint`, `go (build / test / lint)`,
  `govulncheck`, `trivy (image scan)`, `go (integration)`,
  `compose smoke test`, `binaries`, `docker image`, `analyze (go)`.
  PR branches must be up-to-date with `main` before merging.
- **Linear history** &mdash; the merge UI only offers _Squash and
  merge_ and _Rebase and merge_ (`allowed_merge_methods` on the
  `pull_request` rule); plain merge commits would also be rejected
  by `required_linear_history`
- **Signed commits** &mdash; every commit must carry a verified GPG /
  SSH / S/MIME signature (see [Setting up signed commits](#setting-up-signed-commits)
  below); configure a signing key on your GitHub account before
  opening a PR
- **Block force-push and deletion** of `main`
- **Resolve all PR conversations** before merging

### `semver-tag.json` (target: tag)

Applies to every tag matching `refs/tags/v*` (i.e. semver release tags
created by the `release.yaml` workflow) and enforces:

- **Block deletion** &mdash; published releases are immutable;
  `git push --delete origin v1.2.3` is rejected
- **Block force-push** (`non_fast_forward`) &mdash; an existing tag
  cannot be re-pointed at a different commit
- **Linear history** &mdash; included for parity with the manually
  configured ruleset this replaced; on tags it has no behavioural
  effect since tags don't have history of their own

### One-time setup

The workflow needs a token with `Administration: write` on this repo;
the default `GITHUB_TOKEN` deliberately lacks that scope. Steps for
a repo admin:

1. Create a fine-grained PAT scoped to `vancanhuit/url-shortener`
   with `Repository permissions > Administration: Read and write`.
2. Store it as the `RULESETS_TOKEN` repository secret.
3. Trigger the `sync rulesets` workflow once via "Run workflow" so
   the initial ruleset is applied. After that it runs automatically
   whenever anything under `.github/rulesets/` changes on `main`.

### Verifying current state

```sh
# List all active rulesets on the repo:
gh api /repos/vancanhuit/url-shortener/rulesets

# Inspect the live `main` ruleset and diff it against the committed JSON.
# `jq -S` normalizes object-key order on both sides; the JSON is also
# crafted to round-trip the API's server-side defaults (e.g. the
# `pull_request` rule's `required_reviewers: []`), so a clean apply
# produces an empty diff.
gh api "/repos/vancanhuit/url-shortener/rulesets/$(\
    gh api /repos/vancanhuit/url-shortener/rulesets \
        --jq '.[] | select(.name=="main") | .id')" \
    | jq -S '{name, target, enforcement, conditions, bypass_actors, rules}' \
    | diff -u <(jq -S '{name, target, enforcement, conditions, bypass_actors, rules}' \
                   .github/rulesets/main.json) -
```

A non-empty diff means someone changed the ruleset through the UI;
either re-run the `sync rulesets` workflow (to revert) or update the
JSON in a PR (to adopt the change).

### Setting up signed commits

The ruleset rejects unsigned commits on `main`, so every PR must be
signed before it can merge. GPG, SSH, and S/MIME signatures all
satisfy GitHub's verification check; the snippets below cover GPG on
Linux because that's what this repo's maintainer uses. For other
platforms or methods see GitHub's docs on
[signing commits](https://docs.github.com/en/authentication/managing-commit-signature-verification/signing-commits).

> **Note:** the email on your GPG UID must match a verified email on
> your GitHub account, otherwise the signature is mathematically
> valid but GitHub still shows _Unverified_. Check your verified
> emails at [Settings -> Emails](https://github.com/settings/emails).

#### One-time key setup

```sh
# 1. Generate an ed25519 signing key (prompts for a passphrase;
#    `gpg-agent` caches it for the rest of the session).
gpg --quick-generate-key "$(git config --global user.name) <$(git config --global user.email)>" \
    ed25519 sign 2y

# 2. Configure git to sign every commit / tag / rebase output.
KEY_ID=$(gpg --list-secret-keys --keyid-format=long --with-colons "$(git config --global user.email)" \
            | awk -F: '/^sec/ { print $5; exit }')
git config --global gpg.format openpgp
git config --global user.signingkey "$KEY_ID"
git config --global commit.gpgsign true
git config --global tag.gpgsign true
git config --global rebase.gpgsign true

# 3. Make sure pinentry can prompt on the current TTY.
grep -q 'GPG_TTY' ~/.zshrc 2>/dev/null \
    || echo 'export GPG_TTY=$(tty)' >> ~/.zshrc
export GPG_TTY=$(tty)

# 4. Print the armored public key and paste it into
#    https://github.com/settings/gpg/new
gpg --armor --export "$KEY_ID"
```

Verify a fresh commit shows up as **Verified** on the GitHub UI
before merging anything that's gated by the ruleset:

```sh
git checkout -b test/gpg-signing-sanity
git commit --allow-empty -m "chore: gpg signing sanity check"
git log --show-signature -1   # should print "Good signature from ..."
git push -u origin test/gpg-signing-sanity
gh pr view --web              # the commit must show "Verified"
gh pr close --delete-branch
```

#### Re-signing existing commits

If a PR branch was opened before signing was set up, the existing
commits are unsigned and the merge will be rejected. Re-sign in
place and force-push:

```sh
# Single-commit branch -- amend, then force-push.
git commit --amend --no-edit -S
git push --force-with-lease

# Multi-commit branch -- rebase --exec re-creates each commit signed.
# (`rebase.gpgsign=true` from the setup above also signs commits
# created by a plain `git rebase main`, but --exec is explicit.)
git rebase --exec 'git commit --amend --no-edit -S' main
git push --force-with-lease
```

#### Common Linux gotchas

- **`error: gpg failed to sign the data ... Inappropriate ioctl for device`**
  &mdash; `GPG_TTY` is unset in the current shell. Re-run
  `export GPG_TTY=$(tty)`; the snippet above also persists it.
- **Passphrase prompt on every commit** &mdash; `gpg-agent`'s default
  cache TTL is short. Bump it in `~/.gnupg/gpg-agent.conf`:

  ```
  default-cache-ttl 28800
  max-cache-ttl 86400
  ```

  Then `gpgconf --kill gpg-agent` so the next commit picks up the
  new TTL.
- **`gpg: Can't check signature: No public key` on older commits**
  &mdash; harmless; those were signed by GitHub's web-flow key,
  which is just not in your local keyring. Import it once if the
  warning bothers you:

  ```sh
  curl -sS https://github.com/web-flow.gpg | gpg --import
  ```

- **Squash-merge** &mdash; the merge commit is signed by GitHub's
  web-flow key, so `required_signatures` is satisfied even when
  squashing. Squash-merge keeps working unchanged.

## Dependency updates

Automated via [Dependabot](https://docs.github.com/en/code-security/dependabot),
configured in [`.github/dependabot.yml`](.github/dependabot.yml). Five
ecosystems, weekly Monday cadence, grouped per ecosystem so a typical
week produces one PR per group rather than N separate PRs:

| Ecosystem        | Source                                       | Group name                       |
| ---------------- | -------------------------------------------- | -------------------------------- |
| `gomod`          | `go.mod`                                     | `gomod-minor-and-patch`          |
| `github-actions` | `.github/workflows/*.yaml` (SHA-pinned)      | `actions-minor-and-patch`        |
| `npm` (root)     | `package.json` (husky + commitlint)          | `npm-root-minor-and-patch`       |
| `npm` (tailwind) | `web/tailwind/package.json`                  | `npm-tailwind-minor-and-patch`   |
| `docker`         | `Dockerfile` `FROM` lines (golang, node, distroless) | `docker-minor-and-patch` |

Major-version bumps stay outside the groups and arrive as individual
PRs; that's deliberate, since major releases (e.g. `actions/checkout`
v5 dropping Node 16, Go module APIs changing) often need targeted
review against the test suite. Commit messages use the `chore(deps)`
/ `chore(deps-dev)` prefix to satisfy `commitlint`.

### Interaction with the `main` ruleset

Dependabot PRs flow through the same gates as any other PR:

- **Required CI checks** &mdash; the standard `ci.yaml` jobs
  (`go (build / test / lint)`, `compose smoke test`, etc.) run on
  every Dependabot PR; merging is blocked until they all pass.
- **Signed commits** &mdash; Dependabot's commits on the source
  branch are signed by the `dependabot[bot]` GPG key (verified by
  GitHub); the squash-merge commit on `main` is signed by GitHub's
  web-flow key (also verified). `required_signatures` is satisfied
  end-to-end.
- **Up-to-date branch** &mdash; Dependabot rebases its PRs when
  `main` advances, so `strict_required_status_checks_policy: true`
  doesn't strand them.

### Unmanaged: `compose.yaml` images

Dependabot's `docker` ecosystem only scans `Dockerfile`, not
`compose.yaml`. The `postgres` and `redis` image tags in
[`compose.yaml`](compose.yaml) therefore need a manual bump; check
them quarterly or whenever a major version of either ships. The
release-binary `--version` of postgres can be confirmed against the
[official tags page](https://hub.docker.com/_/postgres/tags) before
a bump PR.

## License

To be added.
