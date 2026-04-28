# Justfile -- task runner for the url-shortener project.
# Run `just` (or `just help`) to list recipes.

# Resolve the version string from git tags, matching the documented scheme:
#   git describe --tags --always --dirty --match 'v[0-9]*'
# Tags themselves start with `v`; the binary's version string strips that prefix
# when the working tree is on an exact tag.
VERSION                := `git describe --tags --always --dirty --match 'v[0-9]*' 2>/dev/null | sed -E 's/^v//' || echo "0.0.0-dev"`
COMMIT                 := `git rev-parse --short=12 HEAD 2>/dev/null || echo "unknown"`
DATE                   := `date -u +%Y-%m-%dT%H:%M:%SZ`
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

# Build the binary into ./bin/url-shortener.
build:
    mkdir -p bin
    CGO_ENABLED=0 go build -trimpath -ldflags='{{LDFLAGS}}' -o bin/url-shortener ./cmd/url-shortener

# Run the binary locally.
run *ARGS:
    go run ./cmd/url-shortener {{ARGS}}

# Print the resolved version (useful for verifying the ldflags pipeline).
version:
    @go run ./cmd/url-shortener version 2>/dev/null || echo "Version (resolved): {{VERSION}}"

# Run all unit tests.
test:
    go test -race ./...

# Run unit + integration tests. Integration tests are gated by env vars
# (DATABASE_URL, REDIS_URL) and added in later phases.
test-integration:
    go test -race -tags=integration ./...

# Install golangci-lint v{{GOLANGCI_LINT_VERSION}} into $GOPATH/bin.
# Idempotent: a no-op when the right version is already present.
lint-install:
    @gobin="$(go env GOPATH)/bin"; \
    bin="$gobin/golangci-lint"; \
    want="{{GOLANGCI_LINT_VERSION}}"; \
    have="$([ -x "$bin" ] && "$bin" --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || echo none)"; \
    if [ "$have" = "$want" ]; then \
        echo "golangci-lint $want already installed at $bin"; \
    else \
        echo "installing golangci-lint $want into $gobin (have: $have)"; \
        curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
            | sh -s -- -b "$gobin" "v$want"; \
    fi

# Run linters (auto-installs golangci-lint at the pinned version if missing).
lint: lint-install
    "$(go env GOPATH)/bin/golangci-lint" run

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

# Print commits since a given tag, grouped by conventional-commit type.
# Usage: just changelog-since v0.1.0
changelog-since TAG:
    @echo "## Changes since {{TAG}}"
    @git log {{TAG}}..HEAD --pretty='format:%s' | sort -u | awk -F: ' \
        /^feat(\(.+\))?!?: / {print "\n### Features";   print "- " $0; next} \
        /^fix(\(.+\))?!?: /  {print "\n### Fixes";      print "- " $0; next} \
        /^perf(\(.+\))?!?: / {print "\n### Performance";print "- " $0; next} \
        /^docs(\(.+\))?!?: / {print "\n### Docs";       print "- " $0; next} \
        {print "\n### Other"; print "- " $0}'

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

# Bring up the local stack (db + redis + api). Builds the api image first.
up:
    docker compose up --build -d
    docker compose ps

# Tear down the local stack and remove volumes.
down:
    docker compose down -v

# Tail logs from all compose services (Ctrl-C to exit).
logs *ARGS:
    docker compose logs -f {{ARGS}}

# --- CI -----------------------------------------------------------------------

# Placeholder for the Dagger-driven CI; wired up in a later phase.
ci:
    @echo "CI is not yet implemented (added in the Dagger phase)"
    @exit 1
