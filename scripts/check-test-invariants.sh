#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

violations=0

mapfile -t root_test_files < <(find . -maxdepth 1 -type f -name '*_test.go' | sort)

print_matches() {
  local pattern=$1
  shift
  if [ "$#" -eq 0 ]; then
    return 0
  fi
  grep -nH -E "$pattern" "$@" || true
}

check_no_matches_except_many() {
  local pattern=$1
  shift

  local allowed=()
  while [ "$#" -gt 0 ] && [ "$1" != "--" ]; do
    allowed+=("$1")
    shift
  done
  if [ "$#" -eq 0 ]; then
    echo "check-test-invariants.sh: missing -- separator for pattern $pattern" >&2
    exit 1
  fi
  shift

  local filtered=()
  local file
  local allowed_file
  local keep
  for file in "$@"; do
    keep=true
    for allowed_file in "${allowed[@]}"; do
      if [ "$file" = "$allowed_file" ]; then
        keep=false
        break
      fi
    done
    if [ "$keep" = true ]; then
      filtered+=("$file")
    fi
  done

  local matches
  matches=$(print_matches "$pattern" "${filtered[@]}")
  if [ -n "$matches" ]; then
    echo "test invariant violation: found forbidden pattern '$pattern':"
    echo "$matches"
    echo
    violations=1
  fi
}

# Root CLI subprocess tests must route through the shared hermetic helper.
check_no_matches_except_many 'exec\.Command\(os\.Args\[0\]' \
  './main_cli_subprocess_test.go' \
  -- "${root_test_files[@]}"
check_no_matches_except_many 'AMUX_MAIN_HELPER' \
  './main_cli_subprocess_test.go' \
  -- "${root_test_files[@]}"
check_no_matches_except_many 'os\.Environ\(' \
  './main_cli_subprocess_test.go' './build_install_test.go' \
  -- "${root_test_files[@]}"

if [ "$violations" -ne 0 ]; then
  exit 1
fi

echo "test invariants OK"
