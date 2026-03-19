#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "usage: $0 MICRO_TXT MICRO_EXIT INTEGRATION_TXT INTEGRATION_EXIT" >&2
  exit 2
fi

micro_txt=$1
micro_exit=$2
integration_txt=$3
integration_exit=$4

render_suite() {
  local title=$1
  local txt=$2
  local exit_file=$3
  local rc

  rc=$(cat "$exit_file")

  echo "### $title"
  echo '```'
  if [[ $rc != "0" ]]; then
    echo "suite failed with exit code $rc"
    echo "see uploaded benchmark artifact for raw output"
  elif [[ ! -s $txt ]]; then
    echo "no benchmark output captured"
  elif ! benchstat "$txt"; then
    echo "benchstat could not summarize $txt"
  fi
  echo '```'
  echo ""
}

echo "## Benchmarks"
echo ""
render_suite "Microbenchmarks" "$micro_txt" "$micro_exit"
render_suite "Integration benchmarks" "$integration_txt" "$integration_exit"
