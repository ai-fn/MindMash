# -*- mode: Python -*-
# MindMesh local dev loop. Prereq: run `scripts/cluster-up.sh` first (kind cluster,
# JWT secret, backing stores). Then `tilt up` builds every image, deploys the
# umbrella Helm chart to kind, port-forwards each service, and serves the SPA via
# Vite with HMR at http://localhost:5173.
#
# All config comes from .env (falling back to .env.example) — nothing here is
# hardcoded. See .env.example for the full variable set.

# ── .env loader (self-contained: no external Tilt extension needed) ───────────
def load_env(path):
    env = {}
    if not os.path.exists(path):
        return env
    for line in str(read_file(path)).splitlines():
        line = line.strip()
        if not line or line.startswith('#') or '=' not in line:
            continue
        k, v = line.split('=', 1)
        v = v.split('#')[0].strip().strip('"').strip("'")  # drop inline comment + quotes
        env[k.strip()] = v
    return env

cfg = {}
cfg.update(load_env('.env.example'))   # committed defaults
cfg.update(load_env('.env'))           # developer overrides win

def cget(k, d):
    return cfg.get(k, d)

registry = cget('IMAGE_REGISTRY', 'mindmesh')
tag = cget('IMAGE_TAG', 'dev')
cluster = cget('KIND_CLUSTER_NAME', 'mindmesh-dev')
allow_k8s_contexts('kind-' + cluster)

# ── Host resource guard: build concurrency ──────────────────────────────────
# Tilt builds images in parallel. On a small machine several concurrent Go
# compilations saturate every core and allocate GBs at once, which — on top of the
# always-on backing stores — can drive the host into swap-thrash and freeze it.
# Cap how many images build at once (TILT_MAX_PARALLEL, default 2; set 1 on ≤12 GB).
update_settings(max_parallel_updates=int(cget('TILT_MAX_PARALLEL', '2')))

# ── Service selection ─────────────────────────────────────────────────────────
# Run only a subset of backend services to save CPU/RAM. Precedence (first wins):
#   tilt up identity collection      # per-invocation (positional args)
#   SERVICES=identity,collection     # persistent, in .env
#   (default)                        # all services
# Unselected services are disabled in the Helm release (<svc>.enabled=false below),
# so their pods AND migration Jobs are never created. cluster-up.sh reads the SAME
# SERVICES var to bring up only the backing stores those services need.
ALL_SERVICES = ['identity', 'collection', 'scheduler', 'sync', 'search', 'analytics', 'media']
config.define_string_list('services', args=True)
_tilt_args = config.parse()

def _split_list(s):
    return [x for x in s.replace(',', ' ').split() if x]

selected = _tilt_args.get('services') or _split_list(cget('SERVICES', '')) or ALL_SERVICES
for _s in selected:
    if _s not in ALL_SERVICES:
        fail('unknown service %r; valid: %s (positional, e.g. `tilt up identity collection`)'
             % (_s, ', '.join(ALL_SERVICES)))
print('MindMesh — backend services this session: %s' % ', '.join(selected))
if 'identity' not in selected:
    print('  note: identity not selected; JWKS-validating services may fail at runtime. Add it to SERVICES if you need authenticated calls.')

# ── Images ────────────────────────────────────────────────────────────────────
# Go builds resolve modules through a localhost proxy served from the host module
# cache (scripts/modproxy.sh, started by scripts/cluster-up.sh) so `go mod download`
# stays offline under the dev VPN. --network=host lets the build reach it; the `|`
# in GOPROXY falls back to proxy.golang.org if the proxy is down. GOSUMDB=off skips
# the (network) checksum-db lookup — go.sum still verifies integrity. CI/prod pass
# none of this and keep the Dockerfile's network defaults.
modproxy_port = cget('MODPROXY_PORT', '8877')
go_build_args = {
    'GOPROXY': 'http://127.0.0.1:' + modproxy_port + '|https://proxy.golang.org,direct',
    'GOSUMDB': 'off',
}
# Build only the selected services (see selection block above) — this is the main
# CPU/RAM lever: each Go image build is a full compile.
for svc in selected:
    docker_build(registry + '/' + svc + ':' + tag, context='.', dockerfile='Dockerfile',
                 build_args=dict(go_build_args, SERVICE=svc), network='host')
docker_build(registry + '/migrate:' + tag, context='.', dockerfile='Dockerfile.migrate')
docker_build(registry + '/web:' + tag, context='web', dockerfile='web/Dockerfile')

# ── Helm release ──────────────────────────────────────────────────────────────
# Derive the app DSNs from the SAME .env credentials the dev stores use, so a
# changed password propagates to both the store and the services (single source).
pg_dsn = 'postgres://%s:%s@postgres:5432/%s?sslmode=disable' % (
    cget('POSTGRES_USER', 'mindmesh'), cget('POSTGRES_PASSWORD', 'mindmesh'),
    cget('POSTGRES_DB', 'mindmesh'))

set_args = [
    'global.env.JWT_ISSUER=' + cget('JWT_ISSUER', 'https://dev.mindmesh.local'),
    'global.env.JWKS_ISSUER=' + cget('JWKS_ISSUER', 'https://dev.mindmesh.local'),
    'analytics.secret.data.CLICKHOUSE_PASSWORD=' + cget('CLICKHOUSE_PASSWORD', 'mindmesh'),
]
for svc in ['identity', 'collection', 'scheduler', 'sync', 'media']:
    if svc not in selected:
        continue
    set_args.append('%s.secret.data.DB_DSN=%s' % (svc, pg_dsn))
    set_args.append('%s.migrations.databaseUrl=%s' % (svc, pg_dsn))

# Disable unselected services in the umbrella chart (Chart.yaml gates each subchart on
# `<alias>.enabled`), so their Deployment + migration Job are never rendered/applied.
for svc in ALL_SERVICES:
    if svc not in selected:
        set_args.append('%s.enabled=false' % svc)

k8s_yaml(helm('deploy/helm/mindmesh', name='mindmesh',
              values=['deploy/helm/mindmesh/values-dev.yaml'], set=set_args))

# ── Port-forwards (match web/vite.config.ts proxy convention) ─────────────────
ports = {
    'identity': cget('IDENTITY_PORT', '8080'), 'collection': cget('COLLECTION_PORT', '8081'),
    'scheduler': cget('SCHEDULER_PORT', '8082'), 'sync': cget('SYNC_PORT', '8083'),
    'analytics': cget('ANALYTICS_PORT', '8084'), 'search': cget('SEARCH_PORT', '8085'),
    'media': cget('MEDIA_PORT', '8086'),
}
for svc in selected:
    k8s_resource(svc, port_forwards=ports[svc] + ':8080', labels=['backend'])
k8s_resource('web', port_forwards='8090:8080', labels=['frontend'])

# ── Frontend dev server (Vite, HMR) ──────────────────────────────────────────
# The in-cluster `web` Deployment serves the built image (prod parity). For daily
# work the Vite dev server gives instant HMR against the port-forwarded backends.
#
# Install node_modules first (this worktree starts without them), re-running only
# when package.json / the lockfile change — otherwise `vite` isn't on PATH.
local_resource('web-deps',
               cmd='npm --prefix web ci',
               deps=['web/package.json', 'web/package-lock.json'],
               labels=['frontend'])
local_resource('web-vite',
               serve_cmd='npm --prefix web run dev',
               links=['http://localhost:5173'],
               resource_deps=['web-deps'],
               labels=['frontend'])
