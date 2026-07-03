#!/usr/bin/env bash
# Policy check: every rendered Deployment must have a liveness probe, a readiness
# probe, and container resources.requests/limits (cpu+memory). Unless
# SKIP_PDB_CHECK=1, a PodDisruptionBudget must also exist per Deployment.
#
# Usage: helm template ... | deploy/scripts/policy-check.sh
# Requires: yq (mikefarah, v4) — go install github.com/mikefarah/yq/v4@latest
set -euo pipefail

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
cat > "$tmp"

fail=0
names="$(yq eval -N 'select(.kind == "Deployment") | .metadata.name' "$tmp")"
dep_count=0

while IFS= read -r name; do
  [ -z "$name" ] && continue
  dep_count=$((dep_count + 1))
  doc="$(yq eval -N "select(.kind == \"Deployment\" and .metadata.name == \"$name\")" "$tmp")"

  live="$(yq eval -N '.spec.template.spec.containers[0].livenessProbe // "MISSING"' <<<"$doc")"
  ready="$(yq eval -N '.spec.template.spec.containers[0].readinessProbe // "MISSING"' <<<"$doc")"
  req_cpu="$(yq eval -N '.spec.template.spec.containers[0].resources.requests.cpu // "MISSING"' <<<"$doc")"
  req_mem="$(yq eval -N '.spec.template.spec.containers[0].resources.requests.memory // "MISSING"' <<<"$doc")"
  lim_cpu="$(yq eval -N '.spec.template.spec.containers[0].resources.limits.cpu // "MISSING"' <<<"$doc")"
  lim_mem="$(yq eval -N '.spec.template.spec.containers[0].resources.limits.memory // "MISSING"' <<<"$doc")"

  [ "$live" = "MISSING" ] && { echo "POLICY FAIL: Deployment/$name missing livenessProbe"; fail=1; }
  [ "$ready" = "MISSING" ] && { echo "POLICY FAIL: Deployment/$name missing readinessProbe"; fail=1; }
  if [ "$req_cpu" = "MISSING" ] || [ "$req_mem" = "MISSING" ] || [ "$lim_cpu" = "MISSING" ] || [ "$lim_mem" = "MISSING" ]; then
    echo "POLICY FAIL: Deployment/$name missing resources.requests/limits (cpu+memory)"
    fail=1
  fi
done <<<"$names"

if [ "${SKIP_PDB_CHECK:-0}" != "1" ]; then
  pdb_count="$(yq eval -N 'select(.kind == "PodDisruptionBudget") | .metadata.name' "$tmp" | grep -c . || true)"
  if [ "$pdb_count" -lt "$dep_count" ]; then
    echo "POLICY FAIL: $dep_count Deployment(s) but only $pdb_count PodDisruptionBudget(s)"
    fail=1
  fi
fi

if [ "$fail" -ne 0 ]; then
  echo "Policy check failed."
  exit 1
fi
echo "Policy check passed ($dep_count Deployments checked)."
