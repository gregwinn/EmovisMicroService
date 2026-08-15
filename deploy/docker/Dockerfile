# syntax=docker/dockerfile:1

# Multi-stage: the toolchain never reaches the runtime image.
#
# One Dockerfile builds all three binaries and selects between them with a build
# argument, because they share a module, a dependency graph, and a build cache.
# Three near-identical Dockerfiles would drift.

# ---------------------------------------------------------------------------
# Build
# ---------------------------------------------------------------------------
FROM golang:1.25-alpine AS build

WORKDIR /src

# Dependencies are their own layer: a code change should not re-download the
# module graph.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# Which binary to build. Overridden per service in docker-compose.
ARG BINARY=transaction-api
ARG VERSION=dev

# CGO_ENABLED=0 produces a static binary, which is what makes the distroless
# static base viable.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/app ./cmd/${BINARY}

# ---------------------------------------------------------------------------
# Runtime
# ---------------------------------------------------------------------------
# Distroless static: no shell, no package manager, no libc. Nothing to exploit
# and nothing to patch, which matters for a service on an ingest boundary.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

# Runs as uid 65532. Never root.
USER nonroot:nonroot

COPY --from=build /out/app /app

EXPOSE 8080

ENTRYPOINT ["/app"]
