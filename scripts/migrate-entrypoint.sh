#!/bin/sh
# Self-healing golang-migrate runner (baked into Dockerfile.migrate; invoked by the
# Helm pre-install/pre-upgrade migrate Job).
#
# Usage: migrate-entrypoint.sh <migrations-path> <database-url>
#
# Runs `migrate up`. If the database is wedged in the "dirty" state — a previous
# run was killed between committing a migration and clearing the dirty flag, after
# which plain `migrate up` refuses to run and the Job's retry loop can never
# recover — AND MIGRATE_AUTO_HEAL=true, it forces back to the last clean version
# and re-runs `up`.
#
# Auto-heal is SAFE only because every migration is idempotent (CREATE ... IF NOT
# EXISTS / guarded DO blocks): forcing to version N-1 and re-running `up` re-applies
# a possibly-already-applied migration as a no-op, then continues forward.
#
# Auto-heal is OFF by default. In prod a dirty database is a genuine incident and
# should page a human, not silently self-modify — so values-dev enables it and
# prod/staging leave it off.
set -u

path="${1:?usage: migrate-entrypoint.sh <migrations-path> <database-url>}"
db="${2:?usage: migrate-entrypoint.sh <migrations-path> <database-url>}"

mig() { migrate -path "$path" -database "$db" "$@"; }

out="$(mig up 2>&1)"
rc=$?
printf '%s\n' "$out"

[ "$rc" -eq 0 ] && exit 0
# "no change" (already at the latest version) is success, not failure.
printf '%s' "$out" | grep -q 'no change' && exit 0

# Only auto-heal an actual dirty wedge, and only when explicitly enabled (dev).
# A non-dirty failure (a real SQL error) must still fail loudly.
if [ "${MIGRATE_AUTO_HEAL:-false}" = "true" ] && printf '%s' "$out" | grep -q 'Dirty database version'; then
  cur="$(printf '%s' "$out" | sed -n 's/.*Dirty database version \([0-9][0-9]*\).*/\1/p' | head -1)"
  if [ -n "$cur" ]; then
    prev=$((cur - 1))
    echo "migrate: MIGRATE_AUTO_HEAL — dirty at v${cur}; forcing to v${prev} and re-running up" >&2
    mig force "$prev"
    mig up
    exit $?
  fi
fi

exit "$rc"
