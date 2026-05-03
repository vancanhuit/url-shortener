# Justfile -- task runner for the url-shortener project.
# Run `just` (or `just help`) to list recipes.

# All non-shebang recipes run under bash with strict-mode flags. Shebang
# recipes (`#!/usr/bin/env bash`) bring their own interpreter and re-set
# `pipefail` themselves; this only affects the simple one-liner recipes.
set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

# Resolve the version string from git tags, matching the documented scheme:
#   git describe --tags --always --dirty=-dev --match 'v[0-9]*'
# The leading `v` is preserved so the binary's `version` subcommand prints
# the same string as the git tag and the docker image tag (e.g. `v1.2.3`),
# making it trivial to map a deployed binary back to the source revision.
#
# `--dirty=-dev` overrides the default `-dirty` mark with `-dev`, so a
# local build off a tagged commit with uncommitted edits reports e.g.
# `v1.2.3-dev` -- a clearer "this is a developer build" signal than the
# bare git terminology, while CI runs (always clean checkouts) keep
# emitting the unsuffixed string.
VERSION := `git describe --tags --always --dirty=-dev --match 'v[0-9]*' 2>/dev/null || echo "v0.0.0-dev"`
COMMIT := `git rev-parse --short=12 HEAD 2>/dev/null || echo "unknown"`
# Use the committer timestamp of HEAD (in UTC, RFC3339) so two builds
# of the same commit report identical metadata. Falls back to the
# current wall-clock time outside a git checkout (e.g. tarball builds).
DATE := `TZ=UTC git show -s --format='%cd' --date='format-local:%Y-%m-%dT%H:%M:%SZ' HEAD 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ`
PLATFORMS := "linux/amd64,linux/arm64"
# golangci-lint version. CI overrides this via the GOLANGCI_LINT_VERSION
# env var defined in .github/workflows/ci.yaml so there is a single source of
# truth per run; the literal here is the default for local development.
GOLANGCI_LINT_VERSION := env("GOLANGCI_LINT_VERSION", "2.11.4")
# Trivy version. Installed via the official install.sh into BIN_DIR
# rather than the aquasecurity/trivy-action GitHub Action -- the action
# was compromised in the March-2026 supply-chain incident, so we stick
# to the upstream binary at a pinned version we control.
TRIVY_VERSION := env("TRIVY_VERSION", "0.70.0")
# Install prefix for self-installed tooling (golangci-lint, govulncheck,
# trivy). Defaults to the XDG-style `$HOME/.local/bin`, which is on PATH
# on most modern distros (systemd's user profile, GitHub-hosted runners).
# Override via the `BIN_DIR` env var when a different layout is needed.
# Decoupling from `$(go env GOPATH)/bin` lets non-Go recipes (`trivy-*`,
# `lint-install`) run on hosts that don't have a Go toolchain at all.
BIN_DIR := env("BIN_DIR", env("HOME") + "/.local/bin")
LDFLAGS := "-s -w" + \
    " -X github.com/vancanhuit/url-shortener/internal/buildinfo.version=" + VERSION + \
    " -X github.com/vancanhuit/url-shortener/internal/buildinfo.commit=" + COMMIT + \
    " -X github.com/vancanhuit/url-shortener/internal/buildinfo.date=" + DATE

# Default recipe -- list all available recipes.
default: help

# Show all recipes.
help:
    @just --list --unsorted

# --- setup --------------------------------------------------------------------

# One-time bootstrap: install Node devDependencies (husky + commitlint).
# Uses `npm ci` for deterministic installs when a lockfile is present so CI
# and local environments stay in sync.
[group("setup")]
init:
    @if [ ! -f package.json ]; then \
        echo "package.json missing"; exit 1; \
    fi
    npm ci

# --- dev ----------------------------------------------------------------------

# Build the binary into ./bin/url-shortener. The web UI is embedded via
# `//go:embed`, so this recipe always re-runs `just web-build` first to
# pick up template / CSS-class changes. Tailwind + the htmx vendor copy
# together take ~200ms when npm deps are already installed.
[group("dev")]
build: web-build
    mkdir -p bin
    CGO_ENABLED=0 go build -trimpath -ldflags='{{ LDFLAGS }}' -o bin/url-shortener ./cmd/url-shortener

# Install npm deps for the web tailwind toolchain (idempotent).
[group("setup")]
[working-directory("web/tailwind")]
web-install:
    npm ci

# Compile Tailwind CSS and vendor htmx.min.js into web/static/. The Go
# binary embeds these via `//go:embed` in web/web.go, so re-run this
# after touching templates or CSS classes.
[group("dev")]
[working-directory("web/tailwind")]
web-build: web-install
    npm run build

# Tailwind in watch mode for fast UI iteration. Requires `just up` (or
# `just dev`) so the server is reloading the binary in another terminal.
[group("dev")]
[working-directory("web/tailwind")]
web-watch: web-install
    npm run watch:css

# Run the binary locally.
[group("dev")]
run *ARGS:
    go run ./cmd/url-shortener {{ ARGS }}

# Print the resolved version (useful for verifying the ldflags pipeline).
[group("dev")]
version:
    @go run ./cmd/url-shortener version 2>/dev/null || echo "Version (resolved): {{ VERSION }}"

# Generate a host-trusted dev TLS cert + key into dev/certs/ via mkcert.
# Both files are gitignored; every contributor regenerates locally.
#
# Prerequisites: mkcert (https://github.com/FiloSottile/mkcert).
#   - Linux (apt-based): `sudo apt install -y libnss3-tools && \
#     curl -L -o ~/.local/bin/mkcert "https://dl.filippo.io/mkcert/latest?for=linux/amd64" && \
#     chmod +x ~/.local/bin/mkcert`
#   - macOS:             `brew install mkcert nss`
#
# `mkcert -install` is idempotent and only needs to run once per host
# to add mkcert's local CA to the system + browser trust stores. The
# cert is valid for localhost / 127.0.0.1 / ::1, which covers both the
# `tls`-profile compose stack and a binary running directly on the host.
[group("dev")]
dev-certs:
    #!/usr/bin/env bash
    set -euo pipefail
    if ! command -v mkcert >/dev/null 2>&1; then
        echo 'mkcert not found on PATH; see the recipe doc-comment for install instructions.' >&2
        exit 1
    fi
    mkcert -install >/dev/null
    mkdir -p dev/certs
    mkcert \
        -cert-file dev/certs/cert.pem \
        -key-file  dev/certs/key.pem \
        localhost 127.0.0.1 ::1
    # mkcert writes the key with 0600. The compose `tls` profile mounts
    # dev/certs into a distroless container where the binary runs as
    # UID 65532 (nonroot), so it can't read a host-uid-owned 0600 file.
    # Relax to 0644 -- this is dev-only material, gitignored, never
    # production.
    chmod 0644 dev/certs/key.pem
    echo
    echo "Wrote dev/certs/cert.pem + dev/certs/key.pem (gitignored, 0644 for compose readability)."
    echo "Use them via:"
    echo "    URL_SHORTENER_TLS_CERT_FILE=dev/certs/cert.pem \\"
    echo "    URL_SHORTENER_TLS_KEY_FILE=dev/certs/key.pem \\"
    echo "    ./bin/url-shortener run"
    echo "or with the compose 'tls' profile: docker compose --profile tls up"

# --- test ---------------------------------------------------------------------

# Run all unit tests with verbose output and per-package coverage.
[group("test")]
test:
    go test -race -v -cover ./...

# Run the integration suite end-to-end. Brings up the `test`-profile
# infra (db-test + redis-test on alt ports), applies migrations against
# the test database, and runs the build-tagged tests with the
# URL_SHORTENER_TEST_* env vars set. Tear down with
# `docker compose --profile test down -v` when you're done.
# URLs are hard-coded against the `test`-profile services in compose.yaml
# (db-test on host port 5433, redis-test on 6380); update both files
# together if those ports ever change.
[group("test")]
test-integration: build
    #!/usr/bin/env bash
    set -euo pipefail

    export URL_SHORTENER_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5433/url_shortener?sslmode=disable'
    export URL_SHORTENER_TEST_REDIS_URL='redis://localhost:6380/0'
    docker compose --profile=test up --wait --detach db-test redis-test
    ./bin/url-shortener migrate up --database-url "$URL_SHORTENER_TEST_DATABASE_URL"
    go test -race -v -cover -tags=integration ./...

# --- lint ---------------------------------------------------------------------

# Install golangci-lint v{{GOLANGCI_LINT_VERSION}} into {{BIN_DIR}}.
# Idempotent: a no-op when the right version is already present.
[group("lint")]
lint-install:
    #!/usr/bin/env bash
    set -euo pipefail

    bindir={{ quote(BIN_DIR) }}
    bin="$bindir/golangci-lint"
    want="{{ GOLANGCI_LINT_VERSION }}"
    have="$([ -x "$bin" ] && "$bin" --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || echo none)"
    if [ "$have" = "$want" ]; then
        echo "golangci-lint $want already installed at $bin"
    else
        echo "installing golangci-lint $want into $bindir (have: $have)"
        mkdir -p "$bindir"
        curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
            | sh -s -- -b "$bindir" "v$want"
    fi

# Run linters (auto-installs golangci-lint at the pinned version if missing).
# `--build-tags=integration` is passed so files under `//go:build integration`
# (e.g. internal/store/*_integration_test.go) are linted alongside the rest.
# `-v` makes diagnostics (active linters, build tags, exclusions, timings)
# visible in CI logs without changing the issue output.
[group("lint")]
lint: lint-install web-build tidy
    {{ quote(BIN_DIR / "golangci-lint") }} run -v --build-tags=integration

# Format code (gofumpt + goimports via golangci-lint formatters).
[group("lint")]
fmt: lint-install
    {{ quote(BIN_DIR / "golangci-lint") }} fmt

# --- security -----------------------------------------------------------------

# Run govulncheck against the source. govulncheck is the official Go
# vulnerability scanner: it cross-references our deps against the Go
# vuln database (https://pkg.go.dev/vuln) AND, crucially, only fails
# when a known-bad symbol is actually reachable from one of our
# entrypoints -- so a CVE in a function we never call won't break CI.
# `--build-tags=integration` matches the lint recipe so files behind
# the integration build tag are also analysed.
[group("security")]
govulncheck:
    #!/usr/bin/env bash
    set -euo pipefail

    # Always pull the latest CLI: govulncheck's value is the database
    # it queries, and the CLI itself rarely sees breaking changes worth
    # pinning. `go install ... @latest` is a fast no-op when the binary
    # is already current. Lands in the default GOBIN ($GOBIN, else
    # $(go env GOPATH)/bin) -- govulncheck is a Go tool and the host
    # already needs Go to install it, so the conventional Go layout
    # is the right home (unlike trivy / golangci-lint, which we route
    # through BIN_DIR so they work on hosts without Go).
    gobin="${GOBIN:-$(go env GOPATH)/bin}"
    go install golang.org/x/vuln/cmd/govulncheck@latest
    # `-show=verbose` makes the per-run module + package inventory
    # visible in CI logs, so a failed run is easy to triage and a
    # passing one documents exactly what was scanned (12 root packages,
    # ~30 modules, and the stdlib at the time of writing).
    "$gobin/govulncheck" -show=verbose -tags=integration ./...

# Install trivy v{{TRIVY_VERSION}} into {{BIN_DIR}} via the official
# install.sh. Idempotent: a no-op when the right version is already
# present. We pin to a specific release rather than tracking `latest`
# because trivy is a security-critical binary; reproducible scans
# require a reproducible scanner, and bumps should be intentional.
[group("security")]
trivy-install:
    #!/usr/bin/env bash
    set -euo pipefail

    bindir={{ quote(BIN_DIR) }}
    bin="$bindir/trivy"
    want="{{ TRIVY_VERSION }}"
    have="$([ -x "$bin" ] && "$bin" --version 2>/dev/null | awk '/^Version/ {print $2}' | head -1 || echo none)"
    if [ "$have" = "$want" ]; then
        echo "trivy $want already installed at $bin"
    else
        echo "installing trivy $want into $bindir (have: $have)"
        mkdir -p "$bindir"
        curl -sSfL https://raw.githubusercontent.com/aquasecurity/trivy/main/contrib/install.sh \
            | sh -s -- -b "$bindir" "v$want"
    fi

# Scan an arbitrary image reference (registry tag, digest, or local
# daemon image) with Trivy. Used by both the local `trivy-image`
# recipe (which builds first) and the nightly-scan workflow (which
# scans the already-published GHCR image).
#
# Severity gate: HIGH and CRITICAL fail the run; --ignore-unfixed
# silences findings for which there is no upstream fix yet (we cannot
# act on those, and they otherwise create perpetual noise).
[group("security")]
trivy-scan IMAGE: trivy-install
    {{ quote(BIN_DIR / "trivy") }} image \
        --severity HIGH,CRITICAL \
        --ignore-unfixed \
        --exit-code 1 \
        --no-progress \
        {{ IMAGE }}

# Build the local Docker image (`url-shortener:dev`) and scan it with
# Trivy. Complements `just govulncheck`: govulncheck only sees Go code,
# while Trivy inspects the entire runtime image -- the distroless base,
# the embedded binary, and any OS-level CPEs the registry knows about.
[group("security")]
trivy-image: docker-build
    just trivy-scan url-shortener:dev

# Tidy go.mod / go.sum.
[group("lint")]
tidy:
    go mod tidy

# Smoke-check a running url-shortener stack: hit the operational endpoints
# (`/healthz`, `/readyz`, `/version`), the embedded static assets, and a
# real shorten -> fetch -> redirect cycle. The same script CI's
# `compose-smoke` job runs against the `dev` compose profile after
# `docker compose --profile=dev up --wait`, factored out here so a dev
# can iterate on it locally without round-tripping through GitHub.
#
# Lifecycle stays with the caller: this recipe never starts or stops the
# stack, so re-running it is cheap and never disturbs a long-lived dev
# environment. Bring up the stack first; tear down when you're done:
#
#   docker compose --profile=dev up --wait -d
#   just compose-smoke
#   docker compose --profile=dev down -v
#
# Targets the dev profile's published port directly; the smoke is
# tightly coupled to that stack (it asserts the embedded static
# assets, the shorten/redirect cycle, etc.) so making the URL
# configurable just invited misuse against unrelated environments.
# Requires curl and jq on PATH; both ship preinstalled on the
# GitHub-hosted runners.
[group("test")]
compose-smoke:
    #!/usr/bin/env bash
    set -euo pipefail
    base="http://localhost:8080"

    echo "== /healthz =="
    curl --fail-with-body -sS "$base/healthz" \
        | tee /dev/stderr | jq -e '.status == "ok"' >/dev/null

    echo "== /readyz =="
    curl --fail-with-body -sS "$base/readyz" \
        | tee /dev/stderr \
        | jq -e '.status == "ok" and .postgres == "ok" and .redis == "ok"' >/dev/null

    echo "== /version =="
    curl --fail-with-body -sS "$base/version" \
        | tee /dev/stderr | jq -e 'has("version")' >/dev/null

    # The HTML index references the embedded assets; a missing template
    # parse would 5xx here long before we get a chance to look at the body.
    echo "== / (web index) =="
    body=$(curl --fail-with-body -sS "$base/")
    # Match the rendered <title> rather than the package name -- the
    # template's app title is "URL Shortener" (with a space), and
    # this assertion proves the layout actually rendered rather than
    # serving a stub or a 5xx.
    echo "$body" | grep -qi "<title>URL Shortener</title>" \
        || { echo "index page missing <title>URL Shortener</title>"; exit 1; }

    # The web-builder Dockerfile stage emits styles.css / htmx.min.js /
    # theme.js / copy.js into web/static/, then //go:embed bundles them
    # into the binary. If any of these 404 the embed list and the
    # Dockerfile have drifted apart -- the Go binary would still build
    # clean, which is exactly the regression class this check catches.
    echo "== embedded static assets =="
    for asset in styles.css htmx.min.js theme.js copy.js; do
        curl --fail-with-body -sS -o /dev/null "$base/static/$asset"
    done

    echo "== shorten -> fetch -> redirect =="
    target="https://example.com/smoke/$(date +%s)"
    code=$(curl --fail-with-body -sS -X POST "$base/api/v1/links" \
        -H "content-type: application/json" \
        -d "{\"target_url\":\"$target\"}" | jq -r .code)
    test -n "$code" && test "$code" != null \
        || { echo "create returned no code"; exit 1; }

    # Round-trip the link via the JSON API.
    curl --fail-with-body -sS "$base/api/v1/links/$code" \
        | jq -e --arg t "$target" '.target_url == $t' >/dev/null

    # Public redirect: expect 302 + Location header. -D - dumps response
    # headers; -o /dev/null discards the body. We explicitly do not
    # follow the redirect (no -L) so the status line we inspect is the
    # redirect itself.
    headers=$(curl -sS -D - -o /dev/null "$base/r/$code")
    echo "$headers"
    echo "$headers" | grep -qiE "^HTTP/[0-9.]+ 302" \
        || { echo "expected 302"; exit 1; }
    echo "$headers" | grep -qi "^location: $target" \
        || { echo "expected Location: $target"; exit 1; }

    echo "ok"

# Smoke-test the `tls` compose profile end-to-end with an isolated
# mkcert root CA. Unlike `just dev-certs` this never touches the
# host's trust store (no `mkcert -install`); the leaf cert is
# verified through `curl --cacert` against the just-created root,
# which is exactly the pattern CI needs and a useful regression
# check for the TLS path locally.
#
# What the recipe does, in order:
#
#   1. Allocate a tempdir as CAROOT and mkcert-write the root CA
#      + leaf cert there. Both die with the tempdir at the end.
#   2. Bring up `--profile=tls` with TLS_CERTS_DIR pointing at the
#      tempdir's certs/ subdir, so the existing `dev/certs/`
#      contents are untouched.
#   3. Hit /healthz, /readyz, /version over HTTPS with
#      `--cacert <tempdir>/rootCA.pem` -- a strict check that the
#      server is actually serving the cert we just signed.
#   4. Tear down on exit (success or failure) via trap.
#
# Requires mkcert + curl + jq on PATH. mkcert install instructions
# are in the dev-certs recipe doc-comment.
[group("test")]
tls-smoke:
    #!/usr/bin/env bash
    set -euo pipefail

    if ! command -v mkcert >/dev/null 2>&1; then
        echo 'mkcert not found on PATH; see `just dev-certs` for install instructions.' >&2
        exit 1
    fi

    workdir=$(mktemp -d -t url-shortener-tls-smoke-XXXXXX)
    export TLS_CERTS_DIR="$workdir/certs"
    mkdir -p "$TLS_CERTS_DIR"

    cleanup() {
        local rc=$?
        echo "== teardown =="
        docker compose --profile=tls down -v --remove-orphans >/dev/null 2>&1 || true
        rm -rf "$workdir"
        exit $rc
    }
    trap cleanup EXIT

    echo "== mkcert (isolated CAROOT=$workdir/ca) =="
    # CAROOT redirects mkcert's CA storage; -install is intentionally
    # omitted so the host trust store stays unchanged. The leaf cert
    # is signed for localhost + 127.0.0.1 + ::1 to match what the
    # compose service binds.
    export CAROOT="$workdir/ca"
    mkdir -p "$CAROOT"
    mkcert \
        -cert-file "$TLS_CERTS_DIR/cert.pem" \
        -key-file  "$TLS_CERTS_DIR/key.pem" \
        localhost 127.0.0.1 ::1 >/dev/null
    # Distroless container's nonroot UID needs world-readable key
    # to read it through the bind mount; matches what dev-certs
    # does for the same reason.
    chmod 0644 "$TLS_CERTS_DIR/key.pem"
    cacert="$CAROOT/rootCA.pem"
    test -f "$cacert" || { echo "rootCA.pem not at $cacert" >&2; exit 1; }

    echo "== docker compose --profile=tls up --wait =="
    docker compose --profile=tls up --wait --detach

    base="https://localhost:8443"

    echo "== /healthz (curl --cacert) =="
    curl --fail-with-body -sS --cacert "$cacert" "$base/healthz" \
        | tee /dev/stderr | jq -e '.status == "ok"' >/dev/null

    echo "== /readyz =="
    curl --fail-with-body -sS --cacert "$cacert" "$base/readyz" \
        | tee /dev/stderr \
        | jq -e '.status == "ok" and .postgres == "ok" and .redis == "ok"' >/dev/null

    echo "== /version =="
    curl --fail-with-body -sS --cacert "$cacert" "$base/version" \
        | tee /dev/stderr | jq -e 'has("version")' >/dev/null

    # Negative check: without --cacert (no host-trust install), the
    # request must fail at TLS verification. Proves the certificate
    # chain is genuinely walked rather than e.g. the compose stack
    # serving a default fallback that happens to match.
    echo "== curl without --cacert must fail TLS verification =="
    if curl --fail-with-body -sS -o /dev/null "$base/healthz" 2>/dev/null; then
        echo "expected TLS verification failure, got success" >&2
        exit 1
    fi

    echo "ok"

# --- release ------------------------------------------------------------------

# Lint just the most recent commit message.
[group("release")]
commitlint-last:
    npx --no -- commitlint --from=HEAD~1 --to=HEAD

# Lint a single commit-message string (used by the CI PR-title check).
# Usage: just commitlint-msg "feat: add things"
[group("release")]
commitlint-msg MSG:
    @echo {{ quote(MSG) }} | npx --no -- commitlint

# Generate a release-notes markdown body via git-cliff (cliff.toml at the
# repo root). The release workflow pipes the same output into the GH
# Release body when a new tag is pushed; this recipe is for local preview
# of what a release would say.
#
# git-cliff itself is not pinned in the repo -- install it once:
#
#     # Linux:
#     curl -sSL "https://github.com/orhun/git-cliff/releases/latest/download/\
#     git-cliff-$(uname -m)-unknown-linux-gnu.tar.gz" | tar -xz -C /tmp \
#     && sudo install -m 0755 /tmp/git-cliff-*/git-cliff /usr/local/bin/git-cliff
#
#     # macOS:
#     brew install git-cliff
#
# Output is written to dist/CHANGELOG.md (gitignored, same path the
# release workflow uses) and also echoed to stdout so the recipe
# composes with `| less`, redirection, etc.
#
# Usage:
#   just changelog                    # unreleased commits (latest tag .. HEAD)
#   just changelog v0.1.0             # since v0.1.0 .. HEAD
#   just changelog v0.1.0 v0.2.0      # explicit range
[group("release")]
changelog FROM="" TO="HEAD":
    #!/usr/bin/env bash
    set -euo pipefail
    from='{{ FROM }}'
    to='{{ TO }}'
    if [ -z "$from" ]; then
        # Resolve the latest semver tag reachable from $to. On a fresh repo
        # with no tags yet, fall through to `git-cliff --tag $to` (full
        # history rendered as if tagging now).
        from=$(git describe --tags --abbrev=0 --match 'v[0-9]*' "$to" 2>/dev/null || true)
    fi
    mkdir -p dist
    out=dist/CHANGELOG.md
    if [ -n "$from" ]; then
        git-cliff --config cliff.toml --tag "$to" --output "$out" "$from..$to"
    else
        git-cliff --config cliff.toml --tag "$to" --output "$out"
    fi
    echo "wrote $out:" >&2
    cat "$out"

# --- docker -------------------------------------------------------------------

# Build the Docker image locally for the host's architecture only.
[group("docker")]
docker-build:
    docker build \
        --build-arg VERSION={{ VERSION }} \
        --build-arg COMMIT={{ COMMIT }} \
        --build-arg DATE={{ DATE }} \
        -t url-shortener:{{ VERSION }} \
        -t url-shortener:dev \
        .

# Cross-compile the binary for linux/darwin x amd64/arm64 into ./dist,
# packaging each as a tar.gz with the binary at the root and a
# README.md alongside it. Two flavors of checksum are emitted:
#
#   - per-archive `<archive>.tar.gz.sha256` -- one line, verifiable
#     in isolation with `sha256sum -c <file>.tar.gz.sha256`. Handy
#     when a consumer downloads only one platform's archive.
#   - aggregate `SHA256SUMS` -- one line per archive, verifiable as
#     a set with `sha256sum -c SHA256SUMS`. Handy for mirroring or
#     bulk-verifying everything in one shot.
#
# The release workflow uploads the tarballs + both checksum flavors
# as GitHub Release assets.
#
# `web-build` runs once before the cross-compile loop so all four
# binaries embed the same Tailwind / htmx assets.
#
# Usage:
#   just release-binaries                          # uses VERSION from git describe
#   just --set VERSION v1.2.3 release-binaries     # explicit override
#
# `--set VERSION ...` overrides the global at the top of this Justfile,
# so LDFLAGS (derived from VERSION) and the archive stem both pick up
# the override automatically -- no recipe-level parameter needed.
[group("release")]
release-binaries: web-build
    #!/usr/bin/env bash
    set -euo pipefail

    out=dist
    rm -rf "$out"
    mkdir -p "$out"

    # Pull justfile values into locals once via `quote()` so any
    # special character in the resolved git-describe value is
    # shell-escaped exactly once -- the rest of the recipe then uses
    # ordinary bash expansion ("$version" / "$ldflags") without
    # worrying about further quoting.
    version={{ quote(VERSION) }}
    ldflags={{ quote(LDFLAGS) }}

    for plat in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
        os="${plat%/*}"
        arch="${plat#*/}"
        stem="url-shortener_${version}_${os}_${arch}"
        stage="$out/$stem"
        mkdir -p "$stage"
        echo ">> $plat -> $stage/url-shortener"
        CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
            go build -trimpath -ldflags="$ldflags" \
                -o "$stage/url-shortener" ./cmd/url-shortener
        cp README.md "$stage/"
        tar -C "$out" -czf "$out/${stem}.tar.gz" "$stem"
        # Per-archive checksum sits next to the archive so a consumer
        # who downloads just one platform can verify it without
        # pulling SHA256SUMS too. Run from $out so the recorded path
        # is the bare filename, matching how `sha256sum -c` resolves
        # the target relative to the checksum file's directory.
        (cd "$out" && sha256sum "${stem}.tar.gz" > "${stem}.tar.gz.sha256")
        rm -rf "$stage"
    done

    (cd "$out" && sha256sum *.tar.gz > SHA256SUMS)
    ls -lh "$out"

# Multi-arch build for linux/amd64 + linux/arm64. By default loads nothing
# (buildx cannot --load multi-arch into the local daemon); pass `true`
# as the first argument to publish to a registry.
#
# `--sbom=true` and `--provenance=mode=max` mirror the CI build-push-action
# settings so a local multi-arch build produces the same in-toto
# attestations the registry-pushed image carries. Local single-arch
# `docker-build` skips them: BuildKit can only attach attestations to
# images with the OCI v1.1 manifest layout, which the local docker daemon
# (used by `--load`) does not accept.
#
# `PUSH` is presence-based: pass any non-empty value to publish.
# Examples:
#   just docker-buildx              # local multi-arch build, no push
#   just docker-buildx push         # publish to the registry
[group("docker")]
docker-buildx PUSH="":
    docker buildx build \
        --platform {{ PLATFORMS }} \
        --build-arg VERSION={{ VERSION }} \
        --build-arg COMMIT={{ COMMIT }} \
        --build-arg DATE={{ DATE }} \
        --sbom=true \
        --provenance=mode=max \
        -t url-shortener:{{ VERSION }} \
        {{ if PUSH != "" { "--push" } else { "--output=type=image,push=false" } }} \
        .
