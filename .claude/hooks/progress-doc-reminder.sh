#!/usr/bin/env bash
# PostToolUse hook: after a `git commit` Bash call, remind Claude to check
# whether docs/implementation-progress.md (in the ../MindMesh-docs worktree)
# needs updating. Does nothing for any other Bash command or any non-Bash tool.
set -euo pipefail

payload="$(cat)"

command="$(printf '%s' "$payload" | python3 -c '
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
if data.get("tool_name") != "Bash":
    sys.exit(0)
ti = data.get("tool_input", {}) or {}
print(ti.get("command", ""))
' 2>/dev/null || true)"

[ -z "$command" ] && exit 0
case "$command" in
  *"git commit"*) ;;
  *) exit 0 ;;
esac

cat <<'EOF'
This commit may have completed a tracked milestone (a service's
definition-of-done, a roadmap phase, or a previously user-confirmed
MVP-scoping decision). If so, update docs/implementation-progress.md in the
../MindMesh-docs worktree (checked out to the docs branch) and commit there
— do not push. Most commits are not milestones; use judgment.

If this commit involved deliberately scoping something down (an MVP
decision), only record it if the user already confirmed that scoping in
this conversation — never record an unconfirmed scoping decision.
EOF
exit 0
