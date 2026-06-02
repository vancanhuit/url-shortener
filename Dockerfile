# syntax=docker/dockerfile:1
#
# Multi-stage, multi-arch (linux/amd64, linux/arm64) build for url-shortener.
#
# A single builder stage runs on the build host's native architecture
# (--platform=$BUILDPLATFORM) and cross-compiles the Go binary for
# $TARGETARCH so we never emulate the Go compiler under QEMU. The final
# image is distroless/static nonroot, which is itself a multi-arch manifest.
#
# Toolchain (Go + Bun + Java) is installed via mise so the Docker build
# uses the exact same versions pinned in mise.toml as local development.

# Pin mise to a known release. Tool versions (go, bun, java, ...) are
# resolved by mise from mise.toml.
ARG MISE_VERSION=2026.5.18

# -----------------------------------------------------------------------------
# Builder stage: install mise -> install toolchain -> build SPA -> cross-compile.
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM debian:trixie-slim AS builder

# OS prerequisites for mise itself (downloads + extracts tarballs) and for
# fetching Go modules. Cleaning the apt list saves a few MB in the layer.
RUN export DEBIAN_FRONTEND=noninteractive \
  && apt-get update \
  && apt-get install --no-install-recommends -y \
  ca-certificates \
  curl \
  git \
  xz-utils \
  bash \
  && rm -rf /var/lib/apt/lists/*

# Install mise into /usr/local/bin via the official installer
# (https://mise.jdx.dev/installing-mise.html). MISE_VERSION pins the release
# and MISE_INSTALL_PATH lands the binary directly on $PATH so no post-install
# move is needed.
ARG MISE_VERSION
RUN curl -fsSL https://mise.run | \
  MISE_VERSION=v${MISE_VERSION} MISE_INSTALL_PATH=/usr/local/bin/mise sh

# Activate mise's shims so subsequent RUN steps find go, bun, etc. on PATH.
ENV MISE_DATA_DIR=/root/.local/share/mise
ENV PATH=/root/.local/share/mise/shims:$PATH

WORKDIR /src

# Install the toolchain pinned in mise.toml (go, bun, java, ...). mise.toml
# is the only file we need to copy at this point. Installs land in
# $MISE_DATA_DIR (baked into this layer so `bun`, `go`, etc. are on PATH
# in later steps); only the download cache is mounted to skip re-fetching
# tarballs across builds.
COPY mise.toml ./
RUN --mount=type=cache,target=/root/.cache/mise \
  mise trust mise.toml \
  && mise install \
  && mise reshim

# -----------------------------------------------------------------------------
# Web-assets sub-step: build the Vite + Svelte SPA into web/dist/.
# `web/dist/` is .gitignore'd because the Go binary embeds it via //go:embed,
# so it has to exist before `go build` runs.
# -----------------------------------------------------------------------------
WORKDIR /src/web

# Cache the bun install separately from the rest of the SPA source tree;
# package.json and bun.lock are the only files that gate the install.
COPY web/package.json web/bun.lock ./
RUN --mount=type=cache,target=/root/.bun/install/cache \
  bun install --frozen-lockfile

# Bring in everything Vite needs: SPA source, the index.html shell,
# public/ static-asset overrides, the vendor-docs-assets script,
# tsconfig + svelte.config + vite.config.
COPY web/ ./
RUN bun run build

# -----------------------------------------------------------------------------
# Go build sub-step: cross-compile the Go binary with the SPA assets in place.
# -----------------------------------------------------------------------------
WORKDIR /src

# These are populated by buildx automatically.
ARG TARGETOS
ARG TARGETARCH

# Build metadata injected via -ldflags.
ARG VERSION=v0.0.0-dev
ARG COMMIT=unknown
ARG DATE=1970-01-01T00:00:00Z

# Cache module downloads in a separate layer from the source copy.
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod \
  --mount=type=cache,target=/root/.cache/go-build \
  go mod download

# Copy the rest of the Go source tree. web/dist/ is already populated
# in-place from the previous step, so the //go:embed directive finds it
# without a cross-stage COPY --from=...
COPY . .

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
