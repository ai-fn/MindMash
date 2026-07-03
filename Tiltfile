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

# ── Images ────────────────────────────────────────────────────────────────────
services = ['identity', 'collection', 'scheduler', 'sync', 'search', 'analytics']
for svc in services:
    docker_build(registry + '/' + svc + ':' + tag, context='.', dockerfile='Dockerfile',
                 build_args={'SERVICE': svc})
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
for svc in ['identity', 'collection', 'scheduler', 'sync']:
    set_args.append('%s.secret.data.DB_DSN=%s' % (svc, pg_dsn))
    set_args.append('%s.migrations.databaseUrl=%s&x-migrations-table=schema_migrations_%s' % (svc, pg_dsn, svc))

k8s_yaml(helm('deploy/helm/mindmesh', name='mindmesh',
              values=['deploy/helm/mindmesh/values-dev.yaml'], set=set_args))

# ── Port-forwards (match web/vite.config.ts proxy convention) ─────────────────
ports = {
    'identity': cget('IDENTITY_PORT', '8080'), 'collection': cget('COLLECTION_PORT', '8081'),
    'scheduler': cget('SCHEDULER_PORT', '8082'), 'sync': cget('SYNC_PORT', '8083'),
    'analytics': cget('ANALYTICS_PORT', '8084'), 'search': cget('SEARCH_PORT', '8085'),
}
for svc in services:
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
