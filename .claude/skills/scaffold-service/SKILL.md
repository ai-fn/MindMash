---
name: scaffold-service
description: Use when adding a new backend microservice to the MindMesh monorepo (implementing any docs/detailed/NN-*.md spec). Establishes the contract-first, thin-composition convention so every service has the same shape.
---

# Scaffolding a MindMesh service

Every service is a **thin composition** of `internal/platform/*` + its own `internal/<svc>`
domain. Never reimplement infra (logging/metrics/tracing/db/cache/auth/health) per service.

## Steps (in order)

1. **Read the spec first.** Open the relevant `docs/detailed/NN-<service>.md` and the data
   model in `docs/detailed/10-data-model.md`. Honor its responsibilities, API, schema, events,
   failure modes, and "Build steps" section.

2. **Contract (`api/<svc>/v1/<svc>.proto`).** Define gRPC services + messages. Add
   grpc-gateway HTTP annotations for the external REST routes from the spec. Then run
   `task gen` — Go stubs + OpenAPI are generated, never hand-written.

3. **Migrations (`migrations/<store>/`).** golang-migrate `NNN_name.up.sql` / `.down.sql`.
   Distributed tables carry `user_id` (Citus shard key) and usually `profile_id`; identity
   tables are global. Add the indexes the spec calls out.

4. **Domain (`internal/<svc>/`).** Split into:
   - `domain.go` — entities + pure business logic (unit-testable, no I/O).
   - `repo.go` — persistence via `platform/db` (pgx); always scope queries by `user_id`.
   - `handler.go` — gRPC handlers translating proto ⇄ domain; map errors via `platform/apperr`.
   - `service.go` (optional) — orchestration wiring repo + cache + events.

5. **Entrypoint (`cmd/<svc>/main.go`).** Copy the Identity reference: load config → init
   log/otel/db/cache(/events/blob as needed) → run migrations → wire repo+domain+handlers →
   start gRPC + HTTP gateway → register `/healthz /readyz /metrics` → graceful shutdown.

6. **Cross-cutting:**
   - IDs via `platform/ids` (ULID/UUIDv7).
   - Auth: trust `user_id`/`profile_id` from context injected by `platform/auth` middleware.
   - Async effects via `platform/events` outbox (transactional), idempotent consumers.

7. **Tests:** table-driven unit tests for `domain.go`; `testcontainers-go` integration tests
   for repo + handler against real Postgres/Redis. `buf lint` + `buf breaking` must pass.

8. **Verify:** `task build && task test && task lint`, then run the service against
   `docker compose up` and exercise its REST routes with `curl`.

## Checklist before "done"
- [ ] Proto + generated stubs committed
- [ ] Migrations up/down both apply cleanly
- [ ] Queries scoped by `user_id`
- [ ] Unit + integration tests green
- [ ] `task lint` clean
- [ ] Health/metrics endpoints respond
