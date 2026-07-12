#!/usr/bin/env bash
# Tear down the local dev cluster (stops Tilt first if it's running).
set -euo pipefail

cd "$(dirname "$0")/.."   # repo root
set -a
[ -f .env.example ] && . ./.env.example
[ -f .env ] && . ./.env
set +a
: "${KIND_CLUSTER_NAME:=mindmesh-dev}"

# Best-effort: stop Tilt-managed resources first (ignore if Tilt isn't running).
tilt down >/dev/null 2>&1 || true

# Stop the localhost Go module proxy started by cluster-up.sh.
scripts/modproxy.sh stop >/dev/null 2>&1 || true

echo "==> deleting kind cluster '${KIND_CLUSTER_NAME}'"
kind delete cluster --name "${KIND_CLUSTER_NAME}"
