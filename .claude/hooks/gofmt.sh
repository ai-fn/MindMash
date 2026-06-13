#!/usr/bin/env bash
# PostToolUse hook: format Go files after Claude edits them.
# Reads the hook JSON payload on stdin, extracts the edited file path,
# and runs gofmt + goimports (if available) on it when it's a .go file.
set -euo pipefail

payload="$(cat)"

file_path="$(printf '%s' "$payload" | python3 -c '
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
ti = data.get("tool_input", {}) or {}
print(ti.get("file_path", ""))
' 2>/dev/null || true)"

[ -z "$file_path" ] && exit 0
case "$file_path" in
  *.go) ;;
  *) exit 0 ;;
esac
[ -f "$file_path" ] || exit 0

gofmt -w "$file_path" 2>/dev/null || true
if command -v goimports >/dev/null 2>&1; then
  goimports -w "$file_path" 2>/dev/null || true
fi
exit 0
