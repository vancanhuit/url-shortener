# Justfile -- task runner for the url-shortener project.
# Run `just` (or `just help`) to list recipes.

# Resolve the version string from git tags, matching the documented scheme:
#   git describe --tags --always --dirty --match 'v[0-9]*'
# Tags themselves start with `v`; the binary's version string strips that prefix
# when the working tree is on an exact tag.
VERSION := `git describe --tags --always --dirty --match 'v[0-9]*' 2>/dev/null | sed -E 's/^v//' || echo "0.0.0-dev"`
COMMIT  := `git rev-parse --short=12 HEAD 2>/dev/null || echo "unknown"`
DATE    := `date -u +%Y-%m-%dT%H:%M:%SZ`

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
init:
    @if [ ! -f package.json ]; then \
        echo "package.json missing"; exit 1; \
    fi
    npm install

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

# Run linters (requires golangci-lint v2 in PATH).
lint:
    golangci-lint run

# Format code (gofumpt + goimports via golangci-lint formatters).
fmt:
    golangci-lint fmt

# Tidy go.mod / go.sum.
tidy:
    go mod tidy

# Lint just the most recent commit message.
commitlint-last:
    npx --no -- commitlint --from=HEAD~1 --to=HEAD

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

# Placeholder for the Dagger-driven CI; wired up in a later phase.
ci:
    @echo "CI is not yet implemented (added in the Dagger phase)"
    @exit 1
