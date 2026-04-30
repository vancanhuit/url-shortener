# Justfile -- task runner for the url-shortener project.
# Run `just` (or `just help`) to list recipes.

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
VERSION                := `git describe --tags --always --dirty=-dev --match 'v[0-9]*' 2>/dev/null || echo "v0.0.0-dev"`
COMMIT                 := `git rev-parse --short=12 HEAD 2>/dev/null || echo "unknown"`
# Use the committer timestamp of HEAD (in UTC, RFC3339) so two builds
# of the same commit report identical metadata. Falls back to the
# current wall-clock time outside a git checkout (e.g. tarball builds).
DATE                   := `TZ=UTC git show -s --format='%cd' --date='format-local:%Y-%m-%dT%H:%M:%SZ' HEAD 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ`
PLATFORMS              := "linux/amd64,linux/arm64"
# golangci-lint version. CI overrides this via the GOLANGCI_LINT_VERSION
# env var defined in .github/workflows/ci.yaml so there is a single source of
# truth per run; the literal here is the default for local development.
GOLANGCI_LINT_VERSION  := env("GOLANGCI_LINT_VERSION", "2.11.4")
LDFLAGS := "-s -w" + \
    " -X github.com/vancanhuit/url-shortener/internal/buildinfo.version=" + VERSION + \
    " -X github.com/vancanhuit/url-shortener/internal/buildinfo.commit="  + COMMIT  + \
    " -X github.com/vancanhuit/url-shortener/internal/buildinfo.date="    + DATE

# Default recipe -- list all available recipes.
default: help

# Show all recipes.
help:
    @just --list --unsorted

# One-time bootstrap: install Node devDependencies (husky + commitlint).
# Uses `npm ci` for deterministic installs when a lockfile is present so CI
# and local environments stay in sync.
init:
    @if [ ! -f package.json ]; then \
        echo "package.json missing"; exit 1; \
    fi
    npm ci

# Build the binary into ./bin/url-shortener. The web UI is embedded via
# `//go:embed`, so this recipe always re-runs `just web-build` first to
# pick up template / CSS-class changes. Tailwind + the htmx vendor copy
# together take ~200ms when npm deps are already installed.
build: web-build
    mkdir -p bin
    CGO_ENABLED=0 go build -trimpath -ldflags='{{LDFLAGS}}' -o bin/url-shortener ./cmd/url-shortener

# Install npm deps for the web tailwind toolchain (idempotent).
web-install:
    cd web/tailwind && npm ci

# Compile Tailwind CSS and vendor htmx.min.js into web/static/. The Go
# binary embeds these via `//go:embed` in web/web.go, so re-run this
# after touching templates or CSS classes.
web-build: web-install
    cd web/tailwind && npm run build

# Tailwind in watch mode for fast UI iteration. Requires `just up` (or
# `just dev`) so the server is reloading the binary in another terminal.
web-watch: web-install
    cd web/tailwind && npm run watch:css

# Run the binary locally.
run *ARGS:
    go run ./cmd/url-shortener {{ARGS}}

# Print the resolved version (useful for verifying the ldflags pipeline).
version:
    @go run ./cmd/url-shortener version 2>/dev/null || echo "Version (resolved): {{VERSION}}"

# Run all unit tests with verbose output and per-package coverage.
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
test-integration: build
    #!/usr/bin/env bash
    set -euo pipefail

    export URL_SHORTENER_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5433/url_shortener?sslmode=disable'
    export URL_SHORTENER_TEST_REDIS_URL='redis://localhost:6380/0'
    docker compose --profile=test up --wait --detach db-test redis-test
    ./bin/url-shortener migrate up --database-url "$URL_SHORTENER_TEST_DATABASE_URL"
    go test -race -v -cover -tags=integration ./...

# Install golangci-lint v{{GOLANGCI_LINT_VERSION}} into $GOPATH/bin.
# Idempotent: a no-op when the right version is already present.
lint-install:
    #!/usr/bin/env bash
    set -euo pipefail

    gobin="$(go env GOPATH)/bin"
    bin="$gobin/golangci-lint"
    want="{{GOLANGCI_LINT_VERSION}}"
    have="$([ -x "$bin" ] && "$bin" --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || echo none)"
    if [ "$have" = "$want" ]; then
        echo "golangci-lint $want already installed at $bin"
    else
        echo "installing golangci-lint $want into $gobin (have: $have)"
        curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
            | sh -s -- -b "$gobin" "v$want"
    fi

# Run linters (auto-installs golangci-lint at the pinned version if missing).
# `--build-tags=integration` is passed so files under `//go:build integration`
# (e.g. internal/store/*_integration_test.go) are linted alongside the rest.
# `-v` makes diagnostics (active linters, build tags, exclusions, timings)
# visible in CI logs without changing the issue output.
lint: lint-install web-build tidy
    "$(go env GOPATH)/bin/golangci-lint" run -v --build-tags=integration

# Format code (gofumpt + goimports via golangci-lint formatters).
fmt: lint-install
    "$(go env GOPATH)/bin/golangci-lint" fmt

# Tidy go.mod / go.sum.
tidy:
    go mod tidy

# Lint just the most recent commit message.
commitlint-last:
    npx --no -- commitlint --from=HEAD~1 --to=HEAD

# Lint a single commit-message string (used by the CI PR-title check).
# Usage: just commitlint-msg "feat: add things"
commitlint-msg MSG:
    @echo {{quote(MSG)}} | npx --no -- commitlint

# Generate a release-notes markdown body for commits in (FROM, TO], grouped
# by conventional-commit type. The release workflow pipes this into the GH
# Release body when a new tag is pushed; it's also handy locally to preview
# what a release would say.
#
# Usage:
#   just changelog v0.1.0            # since v0.1.0, up to HEAD
#   just changelog v0.1.0 v0.2.0     # between two tags
changelog FROM TO="HEAD":
    #!/usr/bin/env bash
    set -euo pipefail

    feats=()
    fixes=()
    perfs=()
    refacs=()
    docs_=()
    others=()

    # `tformat:` (vs `format:`) terminates every line with a newline so
    # `read` doesn't drop the final entry when the range has a single
    # commit. `--reverse` so the per-type lists read chronologically.
    while IFS= read -r line; do
        case "$line" in
            "feat:"*|"feat("*)         feats+=("$line") ;;
            "fix:"*|"fix("*)           fixes+=("$line") ;;
            "perf:"*|"perf("*)         perfs+=("$line") ;;
            "refactor:"*|"refactor("*) refacs+=("$line") ;;
            "docs:"*|"docs("*)         docs_+=("$line") ;;
            "build:"*|"build("*|"chore:"*|"chore("*|"ci:"*|"ci("*|"style:"*|"style("*|"test:"*|"test("*) others+=("$line") ;;
        esac
    done < <(git log {{FROM}}..{{TO}} --pretty='tformat:%s' --reverse)

    section() {
        local title="$1"; shift
        if [ "$#" -gt 0 ]; then
            printf '\n### %s\n\n' "$title"
            for it in "$@"; do printf -- '- %s\n' "$it"; done
        fi
    }

    section "Features"    "${feats[@]+"${feats[@]}"}"
    section "Bug Fixes"   "${fixes[@]+"${fixes[@]}"}"
    section "Performance" "${perfs[@]+"${perfs[@]}"}"
    section "Refactors"   "${refacs[@]+"${refacs[@]}"}"
    section "Docs"        "${docs_[@]+"${docs_[@]}"}"
    section "Other"       "${others[@]+"${others[@]}"}"

# --- Docker / compose ---------------------------------------------------------

# Build the Docker image locally for the host's architecture only.
docker-build:
    docker build \
        --build-arg VERSION={{VERSION}} \
        --build-arg COMMIT={{COMMIT}} \
        --build-arg DATE={{DATE}} \
        -t url-shortener:{{VERSION}} \
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
#   just release-binaries           # uses {{VERSION}}
#   just release-binaries v1.2.3    # explicit version override
release-binaries V=VERSION: web-build
    #!/usr/bin/env bash
    set -euo pipefail

    version="{{V}}"
    out=dist
    rm -rf "$out"
    mkdir -p "$out"

    ldflags="-s -w \
        -X github.com/vancanhuit/url-shortener/internal/buildinfo.version=${version} \
        -X github.com/vancanhuit/url-shortener/internal/buildinfo.commit={{COMMIT}} \
        -X github.com/vancanhuit/url-shortener/internal/buildinfo.date={{DATE}}"

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
docker-buildx PUSH="false":
    docker buildx build \
        --platform {{PLATFORMS}} \
        --build-arg VERSION={{VERSION}} \
        --build-arg COMMIT={{COMMIT}} \
        --build-arg DATE={{DATE}} \
        -t url-shortener:{{VERSION}} \
        {{ if PUSH == "true" { "--push" } else { "--output=type=image,push=false" } }} \
        .

# --- CI -----------------------------------------------------------------------

# Placeholder for the Dagger-driven CI; wired up in a later phase.
ci:
    @echo "CI is not yet implemented (added in the Dagger phase)"
    @exit 1
