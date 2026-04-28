# syntax=docker/dockerfile:1.7
#
# Multi-stage, multi-arch (linux/amd64, linux/arm64) build for url-shortener.
#
# The Go builder runs on the build host's native architecture
# (--platform=$BUILDPLATFORM) and cross-compiles for $TARGETARCH so we never
# emulate the Go compiler under QEMU. The final image is distroless/static
# nonroot, which is itself a multi-arch manifest.

ARG GO_VERSION=1.26.2

# -----------------------------------------------------------------------------
# Builder stage: cross-compile the Go binary.
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder

WORKDIR /src

# These are populated by buildx automatically.
ARG TARGETOS
ARG TARGETARCH

# Build metadata injected via -ldflags.
ARG VERSION=0.0.0-dev
ARG COMMIT=unknown
ARG DATE=1970-01-01T00:00:00Z

# Cache module downloads in a separate layer.
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

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
FROM gcr.io/distroless/static-debian13:nonroot AS runtime

WORKDIR /app

COPY --from=builder /out/url-shortener /usr/local/bin/url-shortener

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/url-shortener"]
