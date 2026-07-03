#!/usr/bin/env bash
# Rebuild one image, load it into kind, and restart its Deployment. A non-Tilt
# escape hatch (Tilt does this automatically on save). Because dev images use a
# fixed :dev tag, a plain `helm upgrade` won't re-pull them — hence the explicit
# load + rollout restart.
#
#   scripts/redeploy.sh <identity|collection|scheduler|sync|search|analytics|web|migrate>
set -euo pipefail

cd "$(dirname "$0")/.."   # repo root
set -a
[ -f .env.example ] && . ./.env.example
[ -f .env ] && . ./.env
set +a
: "${KIND_CLUSTER_NAME:=mindmesh-dev}"
: "${IMAGE_REGISTRY:=mindmesh}"
: "${IMAGE_TAG:=dev}"

svc="${1:?usage: scripts/redeploy.sh <identity|collection|scheduler|sync|search|analytics|web|migrate>}"
CTX="kind-${KIND_CLUSTER_NAME}"
img="${IMAGE_REGISTRY}/${svc}:${IMAGE_TAG}"

echo "==> building ${img}"
case "$svc" in
  web)     docker build -t "$img" web ;;
  migrate) docker build -f Dockerfile.migrate -t "$img" . ;;
  *)       docker build --build-arg SERVICE="$svc" -t "$img" . ;;
esac

echo "==> loading into kind"
kind load docker-image "$img" --name "${KIND_CLUSTER_NAME}"

# migrate has no Deployment (it's a Helm hook Job); nothing to restart.
if [ "$svc" != "migrate" ]; then
  echo "==> restarting deployment/${svc}"
  kubectl --context "${CTX}" rollout restart "deployment/${svc}"
  kubectl --context "${CTX}" rollout status "deployment/${svc}" --timeout=120s
fi
