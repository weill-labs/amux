#!/usr/bin/env bash
# Collect merged unit + integration test coverage (used by CI and `make coverage`).
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
unit_args=(-race -coverprofile=unit-coverage.txt -covermode=atomic ./internal/... -timeout 60s)
if [[ "$CI_MODE" == true ]]; then
  go test -json "${unit_args[@]}" | tee unit-results.json || unit_rc=$?
else
  go test "${unit_args[@]}" || unit_rc=$?
fi

# --- Integration tests ---
echo ""
echo "=== Integration tests (GOCOVERDIR) ==="
# Keep the coverage run close to the historical CI shape: parallelism capped
# at 2 for harness stability, no -race on the integration test binary, and no
# AMUX_TEST_RACE in the spawned amux subprocesses. Turning on both coverage and
# race instrumentation here balloons CI wall time because each test harness
# rebuilds and runs a race-enabled amux binary under GOCOVERDIR.
integ_args=(-parallel 2 -timeout 300s ./test/)
if [[ "$CI_MODE" == true ]]; then
  GOCOVERDIR="$COVDIR" go test -json "${integ_args[@]}" | tee integration-results.json || integ_rc=$?
else
  GOCOVERDIR="$COVDIR" go test "${integ_args[@]}" || integ_rc=$?
fi

# --- Merge coverage ---
echo ""
echo "=== Merging coverage ==="
# ServerHarness writes coverage to per-test subdirectories under GOCOVERDIR
# to avoid races between parallel processes. covdata textfmt doesn't recurse,
# so we must list all directories (parent + subdirs) explicitly.
covdirs=$(find "$COVDIR" -type d | paste -sd, -)
if find "$COVDIR" -name 'cov*' -print -quit | grep -q .; then
  go tool covdata textfmt -i="$covdirs" -o=integration-coverage.txt
fi

profiles=()
[[ -f unit-coverage.txt ]] && profiles+=(unit-coverage.txt)
[[ -f integration-coverage.txt ]] && profiles+=(integration-coverage.txt)

if [[ ${#profiles[@]} -gt 0 ]]; then
  # Deduplicate overlapping entries by taking the max count per block.
  # Without this, Codecov undercounts because it sees duplicate entries
  # where one profile has count=0 for a block the other profile covers.
  {
    echo "mode: atomic"
    grep -h -v '^mode:' "${profiles[@]}" \
      | sort -t' ' -k1,1 \
      | awk '{key=$1" "$2; if (key==prev) {if ($3+0 > max) max=$3+0} else {if (NR>1) print prev, max; prev=key; max=$3+0}} END {print prev, max}'
  } > merged-coverage.txt

  go tool cover -func merged-coverage.txt > coverage-summary.txt
  echo ""
  tail -1 coverage-summary.txt
  echo ""
  # Note: this Go-native summary will usually be higher than Codecov's
  # reported percentage for the same uploaded profile. go tool cover reports
  # statement/block coverage, while Codecov normalizes to line coverage and
  # counts partial lines separately.
  awk '!/0.0%/ {print; if (++n == 40) exit}' coverage-summary.txt
fi

# Propagate test failures
if (( unit_rc != 0 || integ_rc != 0 )); then
  exit 1
fi
