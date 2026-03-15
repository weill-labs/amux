#!/usr/bin/env bash
# Collect merged unit + integration test coverage.
#
# Integration tests launch amux as a subprocess inside tmux, so normal
# -coverprofile can't track what the binary executes. This script uses
# GOCOVERDIR for binary-level coverage, then merges both profiles.
#
# Usage:
#   scripts/coverage.sh          # local: run tests, print summary, clean up
#   scripts/coverage.sh --ci     # CI: also emit JSON test results, keep files
set -euo pipefail

CI_MODE=false
if [[ "${1:-}" == "--ci" ]]; then
  CI_MODE=true
fi

COVDIR=$(mktemp -d)
if [[ "$CI_MODE" == false ]]; then
  trap 'rm -rf "$COVDIR" unit-coverage.txt integration-coverage.txt merged-coverage.txt' EXIT
else
  trap 'rm -rf "$COVDIR"' EXIT
fi

# --- Unit tests ---
echo "=== Unit tests ==="
if [[ "$CI_MODE" == true ]]; then
  go test -json -coverprofile=unit-coverage.txt -covermode=atomic ./internal/... -timeout 60s | tee unit-results.json
else
  go test -coverprofile=unit-coverage.txt -covermode=atomic ./internal/... -timeout 60s
fi

# --- Integration tests ---
echo ""
echo "=== Integration tests (GOCOVERDIR) ==="
if [[ "$CI_MODE" == true ]]; then
  GOCOVERDIR="$COVDIR" go test -json -timeout 300s ./test/ | tee integration-results.json
else
  GOCOVERDIR="$COVDIR" go test -timeout 300s ./test/
fi

# --- Merge coverage ---
echo ""
echo "=== Merging coverage ==="
if ls "$COVDIR"/* &>/dev/null; then
  go tool covdata textfmt -i="$COVDIR" -o=integration-coverage.txt
fi

profiles=""
[[ -f unit-coverage.txt ]] && profiles="unit-coverage.txt"
[[ -f integration-coverage.txt ]] && profiles="$profiles integration-coverage.txt"

if [[ -n "$profiles" ]]; then
  {
    echo "mode: atomic"
    grep -v '^mode:' $profiles
  } > merged-coverage.txt

  go tool cover -func merged-coverage.txt > coverage-summary.txt
  echo ""
  tail -1 coverage-summary.txt
  echo ""
  grep -v '0.0%' coverage-summary.txt | head -40
fi
