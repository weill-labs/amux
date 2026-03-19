#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 3 ]]; then
  echo "usage: $0 OUTPUT EXIT_FILE CMD..." >&2
  exit 2
fi

output=$1
exit_file=$2
shift 2

if "$@" | tee "$output"; then
  echo 0 > "$exit_file"
else
  rc=$?
  echo "$rc" > "$exit_file"
fi
