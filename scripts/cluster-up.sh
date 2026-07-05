#!/usr/bin/env bash
# Bootstrap the local MindMesh dev cluster: kind + node DNS fix + JWT secret +
# dev backing stores. Idempotent — safe to re-run. Then run `tilt up`.
#
# Config is read from .env (overriding .env.example defaults). See .env.example.
set -euo pipefail

cd "$(dirname "$0")/.."   # repo root

# Load committed defaults, then the developer's overrides.
set -a
[ -f .env.example ] && . ./.env.example
[ -f .env ] && . ./.env
set +a

: "${KIND_CLUSTER_NAME:=mindmesh-dev}"
CTX="kind-${KIND_CLUSTER_NAME}"

echo "==> kind cluster '${KIND_CLUSTER_NAME}'"
if kind get clusters 2>/dev/null | grep -qx "${KIND_CLUSTER_NAME}"; then
  echo "    already exists, reusing"
else
  kind create cluster --name "${KIND_CLUSTER_NAME}"
fi

# The kind node's resolv.conf can point at Docker's embedded DNS, which fails to
# resolve external registries in some sandboxes; pin a public resolver.
echo "==> node DNS fix"
docker exec "${KIND_CLUSTER_NAME}-control-plane" sh -c \
  'grep -q "8.8.8.8" /etc/resolv.conf || echo "nameserver 8.8.8.8" > /etc/resolv.conf'

echo "==> JWT signing keys + secret"
task keygen
kubectl --context "${CTX}" create secret generic mindmesh-jwt-keys \
  --from-file=ed25519.pem=keys/ed25519.pem \
  --from-file=ed25519.pub.pem=keys/ed25519.pub.pem \
  --dry-run=client -o yaml | kubectl --context "${CTX}" apply -f -

echo "==> dev backing stores (rendered from .env)"
envsubst < deploy/dev/stores.yaml.tmpl | kubectl --context "${CTX}" apply -f -

echo "==> waiting for stores to become ready (up to 3m)"
kubectl --context "${CTX}" wait --for=condition=available --timeout=180s \
  deployment/postgres deployment/redis deployment/redpanda \
  deployment/opensearch deployment/clickhouse deployment/minio

cat <<EOF

Cluster '${KIND_CLUSTER_NAME}' is ready.
  Start app:  tilt up      # build images, deploy, port-forward, serve the SPA (HMR at :5173)
  Stop app:   tilt down
  Delete all: scripts/cluster-down.sh
EOF
