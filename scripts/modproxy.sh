#!/usr/bin/env bash
# Serve the host's Go module cache as a localhost GOPROXY so image builds resolve
# `go mod download` offline instead of hitting proxy.golang.org — which the dev VPN
# resets mid-transfer, stalling `build 5/8` forever.
#
# The host module cache's cache/download/ tree IS already in GOPROXY wire format,
# so a plain static file server is a valid module proxy. Builds pass
#   --network=host --build-arg GOPROXY="http://127.0.0.1:<port>|https://proxy.golang.org,direct"
# (the `|` makes go fall through to the network proxy if this server is down, so a
# missing proxy degrades gracefully instead of breaking the build).
#
#   scripts/modproxy.sh ensure   # start if not already running (idempotent)
#   scripts/modproxy.sh stop     # stop it
#   scripts/modproxy.sh port     # print the port (for scripts/Tiltfile)
set -euo pipefail

cd "$(dirname "$0")/.."   # repo root
set -a
[ -f .env.example ] && . ./.env.example
[ -f .env ] && . ./.env
set +a
: "${MODPROXY_PORT:=8877}"

root="$(go env GOMODCACHE)/cache/download"
pidfile="/tmp/mindmesh-modproxy-${MODPROXY_PORT}.pid"

is_up() { curl -fsS -o /dev/null "http://127.0.0.1:${MODPROXY_PORT}/" 2>/dev/null; }

case "${1:-ensure}" in
  ensure)
    if is_up; then exit 0; fi
    if [ ! -d "$root" ]; then
      echo "modproxy: no host module cache at $root; run 'go mod download' first" >&2
      exit 1
    fi
    # Detached so it outlives this script (Tilt/redeploy builds reuse it all session).
    nohup python3 -m http.server "${MODPROXY_PORT}" --bind 127.0.0.1 --directory "$root" \
      >/tmp/mindmesh-modproxy-${MODPROXY_PORT}.log 2>&1 &
    echo $! > "$pidfile"
    for _ in 1 2 3 4 5 6 7 8 9 10; do is_up && break; sleep 0.3; done
    if is_up; then echo "modproxy: serving $root on 127.0.0.1:${MODPROXY_PORT}"; else
      echo "modproxy: failed to start (see /tmp/mindmesh-modproxy-${MODPROXY_PORT}.log)" >&2; exit 1
    fi
    ;;
  stop)
    [ -f "$pidfile" ] && kill "$(cat "$pidfile")" 2>/dev/null || true
    rm -f "$pidfile"
    echo "modproxy: stopped"
    ;;
  port) echo "${MODPROXY_PORT}" ;;
  *) echo "usage: $0 {ensure|stop|port}" >&2; exit 2 ;;
esac
