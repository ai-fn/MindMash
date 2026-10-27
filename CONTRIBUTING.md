# Contributing

Thanks for considering a contribution! This project welcomes bug reports, feature requests,
documentation improvements, and code. See [`docs/architecture.md`](./docs/architecture.md) for a repo orientation.

## Ways to contribute

- **Bug reports** — found something broken? Open an issue.
- **Feature requests** — have an idea? Open an issue to discuss it before submitting a large PR.
- **Code** — bug fixes, new services/features, performance improvements.
- **Docs** — typo fixes, clarifications, missing examples.
- **Triage** — helping label issues, reproduce bugs, or review open PRs is just as valuable as code.

## Getting started

```bash
git clone https://github.com/ai-fn/MindMash.git
cd MindMash
task tools     # install migrate + golangci-lint (buf/protoc plugins run via `go tool`)
task gen       # buf generate: proto -> Go stubs + OpenAPI
task build     # build all services
```

Requires Go 1.26+, Docker (for the local stack), and Node for the `web/` SPA. The full local
backend stack (Postgres, Redis, Kafka/Redpanda, OpenSearch, ClickHouse, MinIO, Jaeger,
Prometheus, Grafana) runs via `docker-compose.yml`:

```bash
task compose-up     # start the full local stack
task migrate        # apply migrations (identity, collection, scheduler)
task run            # start deps + run the identity service, as an example
```

For running a specific service or the full kind/Tilt dev environment, see
`deploy/README.md`. For the web SPA: `task web-install && task web-dev`.

## Branching & workflow

- Default branch is `main`; active backend implementation happens on `feat/dev-implementation` —
  open code PRs against that branch unless you're told otherwise.
- Branch naming: `feature/<short-description>`, `fix/<short-description>`,
  `docs/<short-description>`.
- Keep PRs focused — one logical change (ideally scoped to a single service) per PR is easier to
  review and merge.
- Rebase your branch on the latest target branch before opening a PR if it's fallen behind.

## Code style

- Contract-first: define/modify protos under `api/<svc>/v1/`, then run `task gen` — never
  hand-edit generated Go stubs or the OpenAPI spec.
- Reuse `internal/platform/*` (config, log, observability, db, cache, events, auth, httpx,
  grpcx, blob, ids, apperr) instead of reimplementing per-service infra.
- Run `task lint` before committing (`go vet` + `golangci-lint` + `buf lint`); CI enforces this.
- Run `goimports`/`gofmt` to auto-format Go code. For `web/`, run `task web-lint`.
- Follow existing patterns in the codebase where the linter doesn't cover the case — the
  core conventions are time-sortable IDs via `platform/ids`, every query scoped by `user_id`, typed
  errors via `platform/apperr`, and cross-service events through the outbox pattern.

## Testing

- Add or update tests for any new functionality or bug fix — unit tests are table-driven;
  integration tests use `testcontainers-go` against real Postgres/Redis/Kafka/etc.
- Run `task test` (full unit + integration suite) before opening a PR, or `task test-short` for a
  quicker unit-only pass.
- `buf lint` / `buf breaking` gate proto changes; Playwright covers `test/e2e`, k6 covers
  `test/load` for the study/answer path.
- CI must pass before a PR will be merged.

## Submitting a pull request

1. Open a PR against the correct target branch (see **Branching & workflow** above) —
   usually `feat/dev-implementation` for backend service work.
2. Fill out the PR template (if present) — describe **what** changed and **why**.
3. Link any related issue(s) with `Closes #123` or `Relates to #123`.
4. A maintainer may request changes — that's normal, not a rejection. A PR is ready to merge once
   it has the required approvals and passing CI.

## Reporting issues

When filing a bug report, please include:

- **Steps to reproduce**
- **Expected behavior**
- **Actual behavior**
- **Environment** (OS, Go version, whether you're on the docker-compose stack or kind/Tilt)

Feature requests should describe the problem you're trying to solve, not just the solution you
have in mind — it makes discussion easier.

## Questions

Not sure where to start, or have a question that isn't a bug or feature request? Open an issue
with the `question` label.
