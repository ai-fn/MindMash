# Deploying MindMesh

Kubernetes/Helm/ArgoCD deployment for the 6 MindMesh backend services (identity, collection,
scheduler, sync, search, analytics) **plus the React SPA frontend** (`web`), implementing
[`docs/detailed/14-kubernetes-deployment.md`](../../MindMesh-docs/docs/detailed/14-kubernetes-deployment.md)
at **pragmatic-foundation** depth: the core topology, scaling, health, rolling updates, secrets,
edge routing, and GitOps patterns are real and exercised; the advanced items (mesh, canary, live
KEDA/ESO, multi-region, OPA) are scaffolded off-by-default or documented as future work — see
[Out of scope](#out-of-scope).

## Running it — two ways

| | Local dev | Production |
|---|---|---|
| **How** | kind + [Tilt](https://tilt.dev): `scripts/cluster-up.sh` then `tilt up` | GitOps: ArgoCD syncs `values-prod.yaml` on push to `main` (see `argocd/README.md`) |
| **Stores** | in-cluster dev stand-ins (`deploy/dev/stores.yaml.tmpl`) | **external managed** services, wired via ExternalSecrets |
| **Frontend** | Vite dev server (HMR) at `localhost:5173` | `web` nginx image behind the edge Ingress at `/` |
| **Config** | `.env` (see [Configuration](#configuration-env)) | ExternalSecrets + CI-written `global.image.tag` |
| **Stop** | `tilt down` (app) · `scripts/cluster-down.sh` (whole cluster) | ArgoCD app disable / `helm uninstall` |

Both are detailed below ([Local dev](#local-dev-kind--tilt), [Production](#production-argocd-gitops)).

## Layout

```
Dockerfile               # ARG SERVICE, multi-stage → gcr.io/distroless/static:nonroot
Dockerfile.migrate       # FROM migrate/migrate + COPY migrations/
web/Dockerfile           # SPA build (node) → nginx serving static dist/ on :8080
Tiltfile                 # local dev loop: build → deploy → port-forward → Vite HMR
.env.example             # all local-dev config knobs (copy to .env)
scripts/
  cluster-up.sh          # kind + node DNS fix + signing-key secrets + dev stores (idempotent)
  cluster-down.sh        # tilt down + delete the kind cluster
  redeploy.sh <svc>      # rebuild+reload one image (non-Tilt escape hatch)
deploy/
  dev/stores.yaml.tmpl   # dev-only in-cluster stores (envsubst from .env) — NOT prod
  helm/
    mindmesh-service/    # the ONE generic chart — one release == one service (incl. web)
    mindmesh/            # umbrella: the generic chart x7 (alias per service + web) + overlays
      values.yaml            # per-service wiring (images, env, secrets, ingress); prod-shaped
      values-dev.yaml        # local/kind: 1 replica, no HPA/PDB, Job-based migrations
      values-staging.yaml    # ExternalSecrets, capped autoscaling, staging.mindmesh.dev
      values-prod.yaml       # ExternalSecrets, TLS, values.yaml's scale as-is
  argocd/                 # AppProject + ApplicationSet (dev/staging/prod) — see argocd/README.md
.github/workflows/deploy.yml  # image build (services + migrate + web) + chart validation (CI)
```

Why an umbrella + one generic subchart instead of 7 charts: every workload shares 100% of the
same template logic (Deployment/Service/HPA/PDB/Ingress/migration Job/...); only *values* differ.
`deploy/helm/mindmesh/Chart.yaml` declares `mindmesh-service` 7 times via `dependencies[].alias`,
and each alias gets its own block in `values.yaml`. The `web` alias reuses the same chart in
HTTP-only mode (`ports.grpc: 0` → no gRPC port, no migration Job, nginx `/healthz` probe).

## Prerequisites

| Tool | Needed for |
|---|---|
| `docker` | building service + migrate images |
| `helm` (v3+) | rendering/installing charts |
| `kubeconform` | static schema validation in CI (`go install github.com/yannh/kubeconform/cmd/kubeconform@latest`) |
| `yq` (mikefarah, v4) | `deploy/scripts/policy-check.sh` (`go install github.com/mikefarah/yq/v4@latest`) |
| `kind` + `kubectl` | local smoke-testing a real rollout |
| ArgoCD + ApplicationSet controller | GitOps delivery (see `argocd/README.md`) |
| External Secrets Operator + a `ClusterSecretStore` | staging/prod secrets (`externalSecrets.enabled: true`) |

## Building images

```bash
docker build --build-arg SERVICE=identity -t mindmesh/identity:dev .
docker build -f Dockerfile.migrate -t mindmesh/migrate:dev .
```

Repeat `SERVICE=` for `collection`, `scheduler`, `sync`, `search`, `analytics`. CI
(`.github/workflows/deploy.yml`) does this for all 6 + migrate in a matrix and pushes to GHCR on
`main`.

## Static validation (also runs in CI)

```bash
cd deploy/helm/mindmesh && helm dependency build && cd -
for env in dev staging prod; do
  helm lint deploy/helm/mindmesh -f deploy/helm/mindmesh/values-$env.yaml
  rendered="$(helm template mindmesh deploy/helm/mindmesh -f deploy/helm/mindmesh/values-$env.yaml)"
  echo "$rendered" | kubeconform -strict -summary -ignore-missing-schemas
  # values-dev.yaml runs single-replica with no PDB by design — skip that one check for it.
  if [ "$env" = dev ]; then
    echo "$rendered" | SKIP_PDB_CHECK=1 deploy/scripts/policy-check.sh
  else
    echo "$rendered" | deploy/scripts/policy-check.sh
  fi
done
```

`-ignore-missing-schemas` is required because `ExternalSecret`/`ScaledObject`/`ServiceMonitor` are
third-party CRDs kubeconform doesn't ship schemas for; they only render when their `*.enabled`
flag is set (off in all three committed overlays except ExternalSecrets in staging/prod).

Alert rules live in `deploy/helm/mindmesh/files/alerts.yaml` and are unit-tested by
`task alerts-test` (promtool, `deploy/prometheus/alerts_test.yml`). Set `prometheusRule.enabled`
(plus `prometheusRule.labels` matching the operator's `ruleSelector`) to ship them as a
`PrometheusRule`; the local compose Prometheus loads the same file. Rules group by the scrape `job`
and assume a generic `TargetDown` alert (e.g. kube-prometheus-stack's) covers services that stop
reporting entirely.

## Local dev (kind + Tilt)

The whole stack — 5 backing stores, 6 services, and the SPA — runs on a local `kind` cluster in
two commands:

```bash
cp .env.example .env          # optional: edit creds / image tags / ports
scripts/cluster-up.sh         # kind cluster + node DNS fix + signing-key secrets + dev stores (idempotent)
tilt up                       # build every image, deploy the chart, port-forward, serve the SPA
```

- Open the Tilt UI at `http://localhost:10350` to watch builds/logs; the app is at
  **`http://localhost:5173`** (Vite dev server, hot-reloading against the port-forwarded backends).
- Each backend is port-forwarded on its own port (identity `:8080` … search `:8085`, matching
  `web/vite.config.ts`), so `curl localhost:8080/healthz` etc. work directly.
- The in-cluster `web` Deployment also runs (serving the built nginx image at `:8090`) for prod
  parity; day-to-day you'll use Vite at `:5173`.
- **Stop:** `tilt down` stops the app but keeps the cluster + stores; `scripts/cluster-down.sh`
  deletes the whole cluster. (`task dev-up` / `task dev` / `task dev-down` wrap these.)
- Changed one service and don't want Tilt? `scripts/redeploy.sh <svc>` rebuilds+reloads just it.

There is **no ingress controller** in the dev cluster — reach services via the port-forwards above,
not the Ingress (which is a prod concern). `docker-compose` (`task compose-up`) remains as a lighter,
non-Kubernetes alternative for pure-backend work.

## Configuration (.env)

Nothing environment-specific is hardcoded in the scripts, the Tiltfile, or committed values —
it all flows from `.env` (falling back to the committed `.env.example` defaults):

```
.env  ──▶  cluster-up.sh (cluster name, stores, secret)
      └──▶  Tiltfile (image registry/tag, and Helm --set for DB DSNs derived from the same creds)
                 └──▶  Helm values ──▶ ConfigMap + Secret ──▶ container env
```

So changing e.g. `POSTGRES_PASSWORD` in `.env` updates both the dev Postgres store and every
service's DSN from one place. `.env` is gitignored; commit changes to `.env.example` only.
In staging/prod the *same variable names* map to ExternalSecret keys, so the contract is identical.

## Production (ArgoCD GitOps)

Prod points at **external managed stores** (RDS/Aurora+Citus, MSK/Redpanda Cloud, ElastiCache,
managed OpenSearch, ClickHouse Cloud); the chart never runs stateful DBs in-cluster. Flow:

Images are pinned by a single **`global.image.tag`** knob (chart helper `mindmesh-service.image`) —
one value tags every service **and** the migrate Job. A per-service `<svc>.image.tag` still overrides
it (dev uses that to pin `:dev`); CI writes the release SHA into the env overlays as below.

1. Push to `main` → CI (`.github/workflows/deploy.yml`) builds, **scans (Trivy)**, and pushes all
   images (6 services + migrate + web) to GHCR, tagged by commit SHA.
2. **Staging (automatic):** the same workflow's `bump-staging-tag` job writes that SHA into
   `values-staging.yaml`'s `global.image.tag` and commits it back to `main` (`[skip ci]`). ArgoCD's
   staging `Application` (auto-sync) rolls the new images.
   **Prod (deliberate):** run the `Deploy prod (promote)` workflow (`.github/workflows/deploy-prod.yml`,
   `workflow_dispatch`) with the SHA to promote — it verifies every image exists, pins
   `values-prod.yaml`'s `global.image.tag`, and commits. A human then runs
   `argocd app sync mindmesh-prod` (prod's Application is `autoSync: false`, so nothing ships until you do).
   > Both write-back jobs push to `main`. If `main` is branch-protected against direct pushes, grant the
   > Actions bot an exception (or switch it to a bot-PR flow) — otherwise the push step fails.
3. Each service's **pre-upgrade migration Job** runs first, then Deployments **roll** (below).
4. Secrets (DB DSNs, ClickHouse password) come from ExternalSecrets; the store contract is: each
   service needs its `DB_DSN` (or `CLICKHOUSE_PASSWORD`) key present in the configured
   `ClusterSecretStore` under `mindmesh/prod/<service>`.

### Rolling updates (zero-downtime)

Every Deployment uses an explicit `RollingUpdate` strategy (`maxUnavailable: 0`, `maxSurge: 25%`)
so a new pod must pass its readiness probe (`/readyz`) before an old one is removed, with a PDB
guarding voluntary disruptions. Because migrations run in a **pre-upgrade Job before** the new
pods roll, schema changes must be **backward-compatible (expand-contract)** — the old pods keep
serving against the migrated schema during the rollout. Tune via `<svc>.strategy` in values.

### Service-to-service keys

Search calls Collection's internal API with its **own** Ed25519 key, never Identity's. Create two
Secrets out-of-band (as for `mindmesh-jwt-keys`): `mindmesh-search-service-key` (`search.pem`,
mounted only into search) and `mindmesh-service-pubkeys` (`search.pub.pem`, mounted into collection
and listed in its `SERVICE_TRUSTED_KEYS=name=path,...`). To rotate, list both keys as
`search=old.pub.pem|new.pub.pem` and roll collection first; only then roll search onto the new
private key, and finally drop the old public key. Rejected service calls are counted in
`mindmesh_service_auth_rejections_total` by method, caller and reason (logs are rate-limited); for
`reason="invalid_token"` the caller is the token's unverified issuer. Service tokens last a minute and are
accepted only for the methods in `collection.InternalServicePolicy`. Tokens aren't bound to a
request and have no replay store, so keep gRPC on a trusted network (or mTLS). Every other service
verifies user JWTs via JWKS only; just Identity mounts the user signing key.

### Media API and worker

One `media` image runs in two Deployments, chosen by `MODE`:

- **`media` (`MODE=api`):** gRPC, REST, the outbox relay and the pending-upload GC.
- **`media-worker` (`MODE=worker`):** consumes `media` events and renders thumbnails. It has no
  gRPC, Ingress or migrations, only probes and `/metrics`.
- **Dev** runs a single `MODE=all` pod and disables `mediaWorker`.

A decode at the pixel cap peaks near 800 MB, and each worker pod decodes one upload at a time.
Its 1.5Gi memory limit therefore covers one decode, plus the raw upload and the runtime. Raise
it if a pod ever decodes more at once. Replicas beyond the topic's partition count sit idle, so
prod caps the HPA at 12.

The bucket needs one-off hardening on AWS. Run this with admin credentials, not the service's:

```bash
deploy/scripts/s3-media-bucket.sh mindmesh-prod-media https://api.mindmesh.dev
```

It sets:

- **Object ownership:** `BucketOwnerEnforced`, so ACLs are off.
- **Public access:** all four Block Public Access flags.
- **Website hosting:** removed.
- **CORS:** the browser's direct PUT, allowing `content-type` and `x-amz-checksum-sha256` from
  the app origin.

MinIO in dev has none of these APIs and allows CORS by default.

### Kafka topics and dead letters

Services create missing topics at startup (`KAFKA_CREATE_TOPICS`, `KAFKA_TOPIC_PARTITIONS`,
`KAFKA_TOPIC_REPLICATION`; prod uses RF 3). Set `KAFKA_CREATE_TOPICS=false` if the services
lack create ACLs and topics are provisioned elsewhere — including every consumed `<topic>.dlq`.

A message whose handler keeps failing ends up in `<topic>.dlq` (30-day retention) with
`dlq_error`/`dlq_group`/`dlq_offset` headers. After fixing the cause, replay it with the
`dlq-replay` image (`docker build --build-arg SERVICE=dlq-replay`), e.g. as a one-off Job:

```bash
dlq-replay -topic card -only-group search-indexer -dry-run   # inspect
dlq-replay -topic card -only-group search-indexer            # republish to `card`
```

A run replays what was in the DLQ when it started and stops there. Progress is committed per
target (`dlq-replay.<topic>.<only-group|all>`, override with `-group`), so reruns resume and
replaying one group never hides another group's dead letters. Replayed messages reach **every**
consumer group of the topic again, after newer events for the same key — handlers must be
idempotent (`event_id`) and tolerate that reordering. Don't mix filtered and unfiltered runs on
one topic: their progress is separate, so the unfiltered run replays the filtered messages again.

### Edge routing

The frontend and API share **one host** so the SPA's relative `/v1/*` fetches are same-origin (no
CORS): the longer `/v1/*` prefixes win over the web `/` catch-all by ingress-nginx longest-match.

| Path | → Service |
|---|---|
| `/v1/cards/{id}/answer`, `/v1/cards/{id}/preview`, `/v1/cards/{id}:reschedule`, `/v1/study` | scheduler (regex; see below) |
| `/v1/cards`, `/v1/decks`, `/v1/notes`, `/v1/note-types`, `/v1/deck-configs`, `/v1/filtered-decks` | collection |
| `/v1/auth`, `/v1/users`, `/v1/profiles`, `/.well-known/jwks.json` | identity |
| `/v1/sync` | sync · `/v1/search` | search · `/v1/stats` | analytics |
| `/` (everything else) | web (SPA) |

Requires ingress-nginx (`nginx.ingress.kubernetes.io/use-regex` splits the scheduler verbs out of
collection's `/v1/cards` prefix). Verify the regex precedence live when standing up a real
controller — it's rendered and static-checked here but the dev cluster has no ingress controller.

### Edge rate limits

Unauthenticated identity endpoints have per-client-IP limits (`limit-rpm`). Each Ingress gets
its own bucket:

| Ingress | Paths | Limit |
|---|---|---|
| `identity-login` | `/v1/auth/login` | 10/min |
| `identity-account` | `/v1/auth/register`, `/v1/auth/password-reset` | 10/min |
| `identity-auth-flows` | `/v1/auth/oauth`, `/v1/auth/verify-email`, `/v1/auth/refresh` | 60/min |

- **Burst.** nginx allows a burst of `limit-rpm × burst-multiplier` (1 for the first two
  Ingresses), so a fresh client gets about twice the limit in its first minute.
- **Shared addresses.** Every user behind one NAT shares a bucket; that cost is why login and
  account creation are bucketed apart.
- **Account lockout.** Identity's own per-account lockout still applies on login and reset.

The controller must be configured for this to work:

- **Real client IP.** Run the controller Service with `externalTrafficPolicy: Local` or
  `use-forwarded-headers` / `proxy-real-ip-cidr` behind a trusted LB. Otherwise every request
  appears to come from the LB and one shared bucket throttles everyone.
- **Status code.** Set `limit-req-status-code: "429"` in the controller ConfigMap (the default
  is 503, which clients would retry as an outage).
- **Counting is per replica.** Each controller replica counts separately, so the effective
  limit is 10 × replicas; size the number for that, or move the limit to a gateway with a
  shared store.

## Known limitations (documented, not silently guessed around)

- **`/v1/cards` prefix overlap** (addressed, needs live confirmation): collection owns
  `/v1/cards` CRUD while scheduler's `/v1/cards/{id}/answer|preview|:reschedule` live under the same
  prefix. Split via scheduler's `use-regex` Ingress (longer/anchored paths win) — see the
  [routing table](#edge-routing). Correct in the rendered manifests; the exact ingress-nginx regex
  precedence should be confirmed against a live controller (the dev cluster has none).
- **OpenSearch security**: disabled in the reference deployment (matches local `docker-compose`).
  A real cluster enabling it needs an `externalSecrets` block for `search`
  (`OPENSEARCH_USERNAME`/`PASSWORD`) that no overlay wires up yet.
- **Search analyzers**: the `cards-v*` index uses only built-in analyzers (`english`/`russian`/
  `cjk`), so the stock `opensearchproject/opensearch:3` image needs no plugins. Upgrading Chinese
  to dictionary segmentation (`analysis-smartcn`/`analysis-icu`) would require a custom image (dev)
  or a managed-service plugin request (prod) plus a reindex behind the `cards` alias — deferred.
- **Migration DSNs in staging/prod**: `<service>.migrations.databaseUrl` is deliberately blank in
  `values-staging.yaml`/`values-prod.yaml` (the pre-install Job no-ops until it's set). Real
  deployments inject it at deploy time from a CD secret store — see `argocd/README.md` — never by
  committing a DSN.
- **ESO secret rotation doesn't roll pods**: the Deployment `checksum/secret` annotation is only
  emitted for the templated dev Secret, so when the External Secrets Operator rotates a staging/prod
  Secret the running pods keep the old env until they restart. A reloader (e.g. stakater/Reloader) or
  a manual `kubectl rollout restart` is needed to pick up rotated secrets.

## Out of scope

Documented as future work, not built here:

- Service mesh (Istio/Linkerd) + mTLS between services.
- Argo Rollouts canary steps gated on Prometheus SLO queries.
- KEDA operator actually installed / live Kafka-lag-driven autoscaling (the `ScaledObject`
  template exists, gated behind `keda.enabled`, and is unexercised).
- External Secrets Operator actually installed, and a secret-plugin (e.g. argocd-vault-plugin)
  wiring the migration DSNs above end-to-end (the `ExternalSecret` CRD template exists and is
  exercised for the app's own `DB_DSN`/`CLICKHOUSE_PASSWORD` in staging/prod).
- Multi-region `ApplicationSet` (cluster generator × region) + GeoDNS + per-region secret stores.
- OPA/Gatekeeper admission policies + chaos-engineering drills.
- Envoy/Kong edge with JWT validation and rate-limiting at the edge (services already validate
  JWTs themselves via cached JWKS, so this is a defense-in-depth/performance layer, not a gap).
