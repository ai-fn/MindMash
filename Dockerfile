# Single parameterized image for every MindMesh Go service.
# Build one service by passing the SERVICE build-arg (the cmd/<SERVICE> dir):
#
#   docker build --build-arg SERVICE=identity -t mindmesh/identity:dev .
#
# Produces a static, non-root, distroless image (no shell, no package manager).

# ---- build stage ---------------------------------------------------------
FROM golang:1.26-alpine AS build

ARG SERVICE
# git + ca-certificates so `go mod download` can reach the module proxy over TLS.
RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Cache dependencies separately from source for faster incremental builds.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

# Fail early with a clear message if the caller forgot --build-arg SERVICE=...
RUN test -n "$SERVICE" || (echo "ERROR: build-arg SERVICE is required (e.g. --build-arg SERVICE=identity)" >&2 && exit 1)

# Fully static binary: CGO off, symbols stripped, reproducible paths. Module + build
# caches are BuildKit cache mounts (not image layers) so repeated `docker build`s across
# the 6 services reuse downloads/compiled packages instead of refetching every time.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath -ldflags="-s -w" \
      -o /out/app ./cmd/${SERVICE}

# ---- runtime stage -------------------------------------------------------
FROM gcr.io/distroless/static:nonroot

# distroless "nonroot" is uid/gid 65532; be explicit so K8s runAsNonRoot is happy.
USER 65532:65532
WORKDIR /

COPY --from=build /out/app /app
# Analytics self-migrates ClickHouse unconditionally at boot (idempotent DDL, no
# RUN_MIGRATIONS gate — see cmd/analytics/main.go), so its runtime image needs the
# SQL on disk regardless of environment. Baking the whole migrations/ tree in is
# harmless for the other services (they never read it unless RUN_MIGRATIONS=true).
COPY --from=build /src/migrations /migrations

# HTTP (REST gateway + /healthz + /readyz + /metrics + jwks) and gRPC.
EXPOSE 8080 50051

ENTRYPOINT ["/app"]
