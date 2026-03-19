#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

violations=0

mapfile -t non_test_go_files < <(find . -type f -name '*.go' ! -name '*_test.go' | sort)
mapfile -t all_go_files < <(find . -type f -name '*.go' | sort)

print_matches() {
  local pattern=$1
  shift
  if [ "$#" -eq 0 ]; then
    return 0
  fi
  grep -nH -E "$pattern" "$@" || true
}

check_no_matches() {
  local pattern=$1
  shift
  local matches
  matches=$(print_matches "$pattern" "$@")
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
  local filtered=()
  local file
  for file in "$@"; do
    if [ "$file" != "$allowed" ]; then
      filtered+=("$file")
    fi
  done
  local matches
  matches=$(print_matches "$pattern" "${filtered[@]}")
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
check_no_matches 'CleanStaleSockets\(' "${non_test_go_files[@]}"
check_no_matches_except 'cleanStaleSocketsIn\(' './internal/server/daemon_test.go' "${all_go_files[@]}"

if [ "$violations" -ne 0 ]; then
  exit 1
fi

echo "startup invariants OK"
