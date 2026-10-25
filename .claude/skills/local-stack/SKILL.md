---
name: local-stack
description: Use when running, testing, or debugging MindMesh services locally — starting dependencies (Postgres/Redis/Kafka/MinIO/etc.), applying migrations, and launching a service. Covers the docker-compose dev workflow.
---

# Running the MindMesh stack locally

## Dependencies (docker-compose)

`docker-compose.yml` defines the full prod-like stack. Start only what you need:

```bash
# Core for Identity / early services:
docker compose up -d postgres redis minio jaeger
# Full stack (adds Kafka, OpenSearch, ClickHouse, Prometheus, Grafana):
docker compose up -d
docker compose ps          # check health
docker compose logs -f <svc>
docker compose down        # stop (add -v to wipe volumes)
```

## Migrations

```bash
task migrate               # apply all up migrations (golang-migrate, reads migrations/<store>/)
```

Install `migrate` if missing: `go install github.com/golang-migrate/migrate/v4/cmd/migrate@latest`.

## Codegen

```bash
task gen                   # buf generate → Go stubs + OpenAPI
```

Install buf + plugins if missing:
`go install github.com/bufbuild/buf/cmd/buf@latest` and the `protoc-gen-*` plugins.

## Run a service

```bash
ENV=dev go run ./cmd/<service>     # e.g. ENV=dev go run ./cmd/identity
```

Config comes from env (12-factor) via `internal/platform/config`; sensible localhost defaults
point at the compose services. `ENV` (dev | staging | prod) is required: the service refuses to
start without it, since dev turns on reflection, boot migrations and the local-key JWT fallback. Every service exposes `/healthz`, `/readyz`, `/metrics`, and the
REST gateway on its configured HTTP port.

## Smoke test (Identity example)

```bash
curl -s localhost:8080/healthz
curl -s localhost:8080/.well-known/jwks.json
curl -s -XPOST localhost:8080/v1/auth/register -d '{"email":"a@b.co","password":"pw","locale":"en"}'
curl -s -XPOST localhost:8080/v1/auth/login    -d '{"email":"a@b.co","password":"pw"}'
```

## Tests

```bash
task test                  # unit + testcontainers integration (needs Docker running)
task lint                  # golangci-lint + buf lint
```
