#!/usr/bin/env bash
set -euo pipefail

repo="weill-labs/amux"

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

need_cmd gh
need_cmd jq
need_cmd unzip
need_cmd python3

pick_run() {
  if [[ $# -gt 0 ]]; then
    echo "$1"
    return
  fi

  local runs_json id
  runs_json=$(gh run list --repo "$repo" --workflow ci.yml --branch main --event push --limit 30 --json databaseId,conclusion)
  while IFS= read -r id; do
    if gh run view "$id" --repo "$repo" --json jobs \
      | jq -e '.jobs[0].steps[] | select(.name=="Tests with coverage" and .conclusion=="success")' >/dev/null; then
      echo "$id"
      return
    fi
  done < <(printf '%s\n' "$runs_json" | jq -r '.[] | select(.conclusion=="success") | .databaseId')

  echo "no successful main push run with coverage found" >&2
  exit 1
}

run_id=$(pick_run "${1:-}")
run_json=$(gh run view "$run_id" --repo "$repo" --json headSha,displayTitle,createdAt)
head_sha=$(printf '%s\n' "$run_json" | jq -r '.headSha')
title=$(printf '%s\n' "$run_json" | jq -r '.displayTitle')
created_at=$(printf '%s\n' "$run_json" | jq -r '.createdAt')

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
zip_path="$tmpdir/logs.zip"
gh api "repos/$repo/actions/runs/$run_id/logs" >"$zip_path"
unzip -q "$zip_path" -d "$tmpdir/logs"

tests_log=$(find "$tmpdir/logs" -type f -name '*Tests with coverage.txt' | head -n 1)
if [[ -z "$tests_log" ]]; then
  echo "could not find Tests with coverage log in run $run_id" >&2
  exit 1
fi

ci_total=$(python3 - "$tests_log" <<'PY'
import re, sys
text = open(sys.argv[1], 'r', encoding='utf-8', errors='replace').read()
matches = re.findall(r'total:\s+\(statements\)\s+([0-9.]+%)', text)
print(matches[-1] if matches else "")
PY
)

codecov_json=$(curl -fsSL "https://api.codecov.io/api/v2/github/weill-labs/repos/amux/branches/main")
codecov_total=$(printf '%s\n' "$codecov_json" | jq -r '.head_commit.totals.coverage')
codecov_sha=$(printf '%s\n' "$codecov_json" | jq -r '.head_commit.commitid')

echo "Run ID:        $run_id"
echo "Run Title:     $title"
echo "Run Created:   $created_at"
echo "Run Commit:    $head_sha"
if [[ -n "$ci_total" ]]; then
  echo "CI Coverage:   $ci_total"
else
  echo "CI Coverage:   not found in run logs"
fi
echo "Codecov Main:  ${codecov_total}%"
echo "Codecov SHA:   $codecov_sha"

if [[ -f coverage-summary.txt ]]; then
  local_total=$(tail -1 coverage-summary.txt | awk '{print $NF}')
  echo "Local Summary: $local_total"
fi

if [[ -n "$ci_total" ]]; then
  python3 - "$ci_total" "$codecov_total" <<'PY'
import sys
ci = float(sys.argv[1].rstrip('%'))
codecov = float(sys.argv[2])
print(f"CI-Codecov Δ:  {ci - codecov:+.2f} pts")
PY
fi
