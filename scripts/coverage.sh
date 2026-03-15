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
set -uo pipefail

CI_MODE=false
if [[ "${1:-}" == "--ci" ]]; then
  CI_MODE=true
fi

COVDIR=$(mktemp -d)
if [[ "$CI_MODE" == true ]]; then
  # CI needs coverage files for Codecov upload and JSON for timing summary
  trap 'rm -rf "$COVDIR"' EXIT
else
  trap 'rm -rf "$COVDIR" unit-coverage.txt integration-coverage.txt merged-coverage.txt' EXIT
fi

# Track exit codes so both suites always run and coverage merges
# even when one suite fails (matching the old CI behavior where
# unit and integration tests were independent workflow steps).
unit_rc=0
integ_rc=0

# --- Unit tests ---
echo "=== Unit tests ==="
unit_args=(-coverprofile=unit-coverage.txt -covermode=atomic ./internal/... -timeout 60s)
if [[ "$CI_MODE" == true ]]; then
  go test -json "${unit_args[@]}" | tee unit-results.json || unit_rc=$?
else
  go test "${unit_args[@]}" || unit_rc=$?
fi

# --- Integration tests ---
echo ""
echo "=== Integration tests (GOCOVERDIR) ==="
integ_args=(-timeout 300s ./test/)
if [[ "$CI_MODE" == true ]]; then
  GOCOVERDIR="$COVDIR" go test -json "${integ_args[@]}" | tee integration-results.json || integ_rc=$?
else
  GOCOVERDIR="$COVDIR" go test "${integ_args[@]}" || integ_rc=$?
fi

# --- Merge coverage ---
echo ""
echo "=== Merging coverage ==="
if compgen -G "$COVDIR/*" >/dev/null; then
  go tool covdata textfmt -i="$COVDIR" -o=integration-coverage.txt
fi

profiles=()
[[ -f unit-coverage.txt ]] && profiles+=(unit-coverage.txt)
[[ -f integration-coverage.txt ]] && profiles+=(integration-coverage.txt)

if [[ ${#profiles[@]} -gt 0 ]]; then
  {
    echo "mode: atomic"
    grep -v '^mode:' "${profiles[@]}"
  } > merged-coverage.txt

  go tool cover -func merged-coverage.txt > coverage-summary.txt
  echo ""
  tail -1 coverage-summary.txt
  echo ""
  grep -v '0.0%' coverage-summary.txt | head -40
fi

# Propagate test failures
if (( unit_rc != 0 || integ_rc != 0 )); then
  exit 1
fi
