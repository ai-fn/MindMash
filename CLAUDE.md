# CLAUDE.md

Guidance for working in the **MindMesh** repository.

## What this is

MindMesh is a greenfield, **Anki-like spaced-repetition flashcards web platform** built for
global scale (1M+ users). It is a **Go microservices monorepo** on Kubernetes with polyglot
persistence. The design is **server-authoritative** (the backend owns FSRS scheduling; clients
only submit review grades) and **web-only** (offline-first is intentionally descoped).

The authoritative blueprint lives in [`docs/`](docs/):
- [`docs/architecture.md`](docs/architecture.md) — system overview, service catalog, build order.
- [`docs/detailed/01..19`](docs/detailed/) — per-component implementation specs (the source of
  truth for each service). Always read the relevant `docs/detailed/NN-*.md` before implementing
  a service.
- [`functional-requirements.md`](functional-requirements.md) — product requirements.

Implementation is executed **one detailed spec at a time**, in roadmap order
([`docs/detailed/19-build-roadmap.md`](docs/detailed/19-build-roadmap.md)): Phase 0 foundations,
then Identity → Collection → Scheduler → Sync → … .

## Stack

Go 1.26 · gRPC + grpc-gateway (REST) · buf (proto lint/breaking/codegen) · PostgreSQL 16
(+Citus, sharded by `user_id`; identity tables are global/non-sharded) · Redis · Kafka/Redpanda ·
OpenSearch · ClickHouse · S3/MinIO · OpenTelemetry/Jaeger + Prometheus/Grafana + ELK ·
Frontend (later): React + TypeScript + Vite + i18next.

## Repository layout

```
api/                  # protobuf contracts (source of truth) + buf config; codegen → Go stubs + OpenAPI
cmd/<service>/        # thin main.go entrypoint per service binary
internal/
  <service>/          # domain, handlers, repositories per service (NOT importable cross-service)
  platform/           # SHARED libs: config, log, observability, db, cache, events, auth, httpx, grpcx, blob, ids, errors
pkg/                  # reusable importable libs (fsrs, template, searchdsl)
migrations/<store>/   # golang-migrate SQL per store
deploy/               # helm/kustomize + argocd (spec 14)
web/                  # React SPA (spec 12)
test/                 # e2e (Playwright), load (k6), contract tests
docs/                 # architecture + detailed specs
```

## Core conventions (apply to every service)

- **Contract-first:** define the proto in `api/<svc>/v1/`, then `task gen`. Services and the TS
  client are **generated**, never hand-drifted. gRPC internally; REST/JSON externally via
  grpc-gateway annotations.
- **Thin composition:** a service = `internal/platform/*` + its `internal/<svc>` domain. Do not
  reimplement logging/metrics/tracing/db/cache/auth/health per service — use `platform`.
- **IDs:** ULID/UUIDv7 (time-sortable) via `platform/ids` for all entities.
- **Errors:** typed app errors via `platform/errors`, mapped to gRPC status / HTTP codes.
- **Tenancy:** every distributed row carries `user_id` (Citus shard key); every query is scoped
  by it. Most rows also carry `profile_id`. Identity mints the `user_id`/`profile_id`.
- **Auth:** every external call carries a JWT (Ed25519). The gateway validates it via cached
  JWKS **without** calling Identity per request, and injects `user_id`/`profile_id`/`locale`/
  `roles` as trusted context/metadata downstream.
- **Events:** cross-service async effects flow over Kafka using the **transactional outbox**
  pattern (`outbox` table) with idempotency keys — keep request paths fast.

## Common commands (Taskfile)

```
task gen       # buf generate (proto → Go stubs + OpenAPI)
task build     # build all services
task test      # unit + integration tests
task lint      # golangci-lint + buf lint
task migrate   # apply golang-migrate migrations
task run       # docker compose up + run services locally
```

Local dev tools not vendored (install via `go install`): `buf`, `protoc-gen-go`,
`protoc-gen-go-grpc`, `protoc-gen-grpc-gateway`, `protoc-gen-openapiv2`, `golang-migrate`.

Local stack via `docker-compose.yml`: Postgres, Redis, Kafka/Redpanda, OpenSearch, ClickHouse,
MinIO, Jaeger, Prometheus, Grafana.

## Testing

- **Unit:** table-driven, per package.
- **Integration:** `testcontainers-go` spins real Postgres/Redis/Kafka/etc.
- **Contract:** generated stubs validate client/server alignment; `buf lint` + `buf breaking` in CI.
- **E2E:** Playwright; **Load:** k6 on the study/answer path.

Run `task test` and `task lint` before claiming work complete; never assert success without
seeing the command output.

## Definition of done per service

Proto + migrations + domain + handlers + caching + events + unit/integration tests + Helm chart
+ dashboards/alerts + `docs/detailed` entry honored. (Helm/dashboards land with their platform
phases; don't block a service's core on them unless that phase is in scope.)

## Git

- Default branch: `main`. Active work branch: `feat/dev-implementation`. Detailed specs live on
  the `docs` branch.
- Commit/push only when asked. End commit messages with the required `Co-Authored-By` trailer.
