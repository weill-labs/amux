#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

violations=0

non_test_go_files=$(rg --files --glob '*.go' --glob '!**/*_test.go')

check_no_matches() {
  local pattern=$1
  shift
  local matches
  matches=$(printf '%s\n' "$@" | xargs rg -n "$pattern" 2>/dev/null || true)
  if [ -n "$matches" ]; then
    echo "startup invariant violation: found forbidden pattern '$pattern':"
    echo "$matches"
    echo
    violations=1
  fi
}

check_no_matches_except() {
  local pattern=$1
  local allowed=$2
  shift 2
  local matches
  matches=$(printf '%s\n' "$@" | grep -v "^${allowed}$" | xargs rg -n "$pattern" 2>/dev/null || true)
  if [ -n "$matches" ]; then
    echo "startup invariant violation: found forbidden pattern '$pattern':"
    echo "$matches"
    echo
    violations=1
  fi
}

# Broad stale-socket sweeping must stay out of production code. It is only
# safe in explicitly-scoped test helpers because parallel server startup can
# otherwise unlink another live session socket.
check_no_matches 'CleanStaleSockets\(' "$non_test_go_files"
check_no_matches_except 'cleanStaleSocketsIn\(' 'internal/server/daemon_test.go' $(rg --files --glob '*.go')

if [ "$violations" -ne 0 ]; then
  exit 1
fi

echo "startup invariants OK"
