#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 INPUT OUTPUT" >&2
  exit 2
fi

input=$1
output=$2
filtered=$(mktemp)
trap 'rm -f "$filtered"' EXIT

if grep -q -- '--- FAIL: Benchmark' "$input"; then
  echo "benchmark failure markers found in $input" >&2
  exit 1
fi

# Drop package summary noise that gobenchdata cannot parse, but preserve
# the standard PASS/ok footer that Go benchmark output normally includes.
grep -v -E '^\?[[:space:]].*\[no test files\]$' "$input" > "$filtered" || true

if ! grep -q '^Benchmark' "$filtered"; then
  echo "no benchmark lines found in $input" >&2
  exit 1
fi

cat "$filtered" | gobenchdata --json "$output"
