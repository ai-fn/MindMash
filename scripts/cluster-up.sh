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

echo "==> signing keys + secrets"
task keygen
kubectl --context "${CTX}" create secret generic mindmesh-jwt-keys \
  --from-file=ed25519.pem=keys/ed25519.pem \
  --from-file=ed25519.pub.pem=keys/ed25519.pub.pem \
  --dry-run=client -o yaml | kubectl --context "${CTX}" apply -f -
# Search's private key reaches only search; collection gets just its public half.
kubectl --context "${CTX}" create secret generic mindmesh-search-service-key \
  --from-file=search.pem=keys/search-service.pem \
  --dry-run=client -o yaml | kubectl --context "${CTX}" apply -f -
kubectl --context "${CTX}" create secret generic mindmesh-service-pubkeys \
  --from-file=search.pub.pem=keys/search-service.pub.pem \
  --dry-run=client -o yaml | kubectl --context "${CTX}" apply -f -

# Render once; reuse for both preload and apply so the image list and the manifest
# never drift.
rendered="$(envsubst < deploy/dev/stores.yaml.tmpl)"

# ── Which backing stores to run ───────────────────────────────────────────────
# Bring up only the stores the selected services need. SERVICES is the SAME knob the
# Tiltfile reads (comma/space list; empty = all). STORES overrides the derivation
# explicitly.
declare -A _svc_stores=(
  [identity]="postgres"
  [collection]="postgres"
  [scheduler]="postgres redis"
  [sync]="postgres redpanda"
  [search]="opensearch redpanda"
  [analytics]="clickhouse redpanda"
  [media]="postgres minio"
)
_all_svcs="identity collection scheduler sync search analytics media"
_sel_svcs="${SERVICES:-}"; _sel_svcs="${_sel_svcs//,/ }"
[ -n "${_sel_svcs// }" ] || _sel_svcs="$_all_svcs"
if [ -n "${STORES:-}" ]; then
  need_stores="${STORES//,/ }"
else
  need_stores=""
  for s in $_sel_svcs; do need_stores="$need_stores ${_svc_stores[$s]:-}"; done
fi
need_stores="$(printf '%s\n' $need_stores | sort -u | grep -v '^$' | tr '\n' ' ')"
echo "==> backing stores this session: ${need_stores}"

# Keep only the YAML docs (Deployment + Service) whose `app:` label is in the set,
# so preload/apply/wait below touch just the selected stores.
rendered="$(printf '%s\n' "$rendered" | awk -v want="$need_stores" '
  BEGIN { n = split(want, a, " "); for (i = 1; i <= n; i++) W[a[i]] = 1 }
  function flush() { if (keep) printf "%s", buf; buf = ""; keep = 0 }
  /^---[ \t]*$/ { flush(); buf = $0 ORS; next }
  {
    buf = buf $0 ORS
    if (match($0, /app:[ \t]*"?[a-z0-9-]+/)) {
      s = substr($0, RSTART, RLENGTH); sub(/app:[ \t]*"?/, "", s)
      if (s in W) keep = 1
    }
  }
  END { flush() }')"

# Preload store images into the kind node from the host Docker cache. The host
# `docker pull` retries far better than the node's containerd (which the dev VPN
# resets mid-pull, stranding pods in ImagePullBackOff), and often the image is
# already cached locally. We import straight into the node's containerd rather than
# `kind load docker-image`: kind's loader runs `ctr import --all-platforms --digests`,
# which fails ("content digest not found") when the host Docker uses the containerd
# image store. A plain `docker save | ctr import` imports the single image cleanly.
# Tags are pinned, so the default IfNotPresent policy then skips the in-cluster pull.
node="${KIND_CLUSTER_NAME}-control-plane"
echo "==> preloading store images (host pull + containerd import)"
echo "${rendered}" | awk '/image:/{print $2}' | tr -d '"' | sort -u | while read -r img; do
  [ -n "${img}" ] || continue
  if docker pull "${img}" >/dev/null 2>&1 &&
     docker save "${img}" | docker exec --privileged -i "${node}" \
       ctr -n k8s.io images import --snapshotter=overlayfs - >/dev/null 2>&1; then
    echo "    loaded ${img}"
  else
    echo "    warn: preload failed for ${img} (falling back to in-cluster pull)"
  fi
done

echo "==> dev backing stores (rendered from .env)"
echo "${rendered}" | kubectl --context "${CTX}" apply -f -

# Start the localhost Go module proxy (host cache) so the upcoming `tilt up` image
# builds resolve `go mod download` offline instead of stalling on the VPN.
echo "==> module proxy for offline Go builds"
scripts/modproxy.sh ensure || echo "    warn: modproxy not started; Go builds will use the network proxy"

echo "==> waiting for stores to become ready (up to 3m)"
for s in ${need_stores}; do
  kubectl --context "${CTX}" wait --for=condition=available --timeout=180s "deployment/${s}"
done

cat <<EOF

Cluster '${KIND_CLUSTER_NAME}' is ready.
  Start app:  tilt up      # build images, deploy, port-forward, serve the SPA (HMR at :5173)
  Stop app:   tilt down
  Delete all: scripts/cluster-down.sh
EOF
