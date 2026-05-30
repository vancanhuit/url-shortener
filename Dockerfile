# syntax=docker/dockerfile:1.7
#
# Multi-stage, multi-arch (linux/amd64, linux/arm64) build for url-shortener.
#
# The Go builder runs on the build host's native architecture
# (--platform=$BUILDPLATFORM) and cross-compiles for $TARGETARCH so we never
# emulate the Go compiler under QEMU. The final image is distroless/static
# nonroot, which is itself a multi-arch manifest.

ARG GO_VERSION=1.26.3
ARG NODE_VERSION=24.16.0

# -----------------------------------------------------------------------------
# Web-assets stage: build the Vite + Svelte SPA into web/dist/.
# `web/dist/` is .gitignore'd because the Go binary embeds it via
# `//go:embed` -- so it has to exist before the Go builder runs.
# Pinned to BUILDPLATFORM since the toolchain is JS, not arch-specific.
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-trixie-slim AS web-builder

WORKDIR /src/web

# Cache npm install separately from the rest of the SPA source tree;
# `package*.json` are the only files that gate the install.
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci

# Bring in everything Vite needs: SPA source, the index.html shell,
# `public/` static-asset overrides, the vendor-docs-assets script,
# tsconfig + svelte.config + vite.config.
COPY web/ ./
RUN npm run build

# -----------------------------------------------------------------------------
# Builder stage: cross-compile the Go binary.
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-trixie AS builder

WORKDIR /src

# These are populated by buildx automatically.
ARG TARGETOS
ARG TARGETARCH

# Build metadata injected via -ldflags.
ARG VERSION=v0.0.0-dev
ARG COMMIT=unknown
ARG DATE=1970-01-01T00:00:00Z

# Cache module downloads in a separate layer.
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .

# Pull the Vite SPA build output (`dist/`) into the Go source tree so
# the //go:embed directive finds it. The directory contains the SPA
# shell (index.html), Vite's hashed bundles under assets/, and the
# vendored Swagger UI + Redoc files under static/ (used by the API
# docs viewers at /api/v1/docs and /api/v1/redoc).
COPY --from=web-builder /src/web/dist ./web/dist

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
        -trimpath \
        -ldflags="-s -w \
            -X github.com/vancanhuit/url-shortener/internal/buildinfo.version=${VERSION} \
            -X github.com/vancanhuit/url-shortener/internal/buildinfo.commit=${COMMIT} \
            -X github.com/vancanhuit/url-shortener/internal/buildinfo.date=${DATE}" \
        -o /out/url-shortener \
        ./cmd/url-shortener

# -----------------------------------------------------------------------------
# Runtime stage: distroless static, nonroot.
# -----------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian13:nonroot

# Static OCI image labels. Dynamic ones (version, revision, created)
# are emitted per build by docker/metadata-action in CI; baking the
# fixed identity here means `docker build` outside CI inherits them
# too. See https://github.com/opencontainers/image-spec/blob/main/annotations.md
LABEL org.opencontainers.image.title="url-shortener" \
      org.opencontainers.image.description="A small URL-shortener service with a JSON API and Svelte web UI." \
      org.opencontainers.image.source="https://github.com/vancanhuit/url-shortener" \
      org.opencontainers.image.url="https://github.com/vancanhuit/url-shortener" \
      org.opencontainers.image.documentation="https://github.com/vancanhuit/url-shortener#readme" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.vendor="vancanhuit"

WORKDIR /app

COPY --from=builder /out/url-shortener /usr/local/bin/url-shortener

EXPOSE 8080

USER nonroot:nonroot

# Probe the binary's own /livez via the `healthcheck` subcommand.
# Distroless ships neither curl nor wget, so the binary itself is the
# probe. Mirrors the compose.yaml healthcheck so `docker run` users get
# the same liveness signal without composing a stack. Kubernetes and
# other orchestrators that drive their own probes can override this
# with their own livenessProbe.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/url-shortener", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/url-shortener"]
