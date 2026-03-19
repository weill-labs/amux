#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 9 ]]; then
  echo "usage: $0 OUT HEAD_SHA RUN_ID RUN_ATTEMPT RUNNER_OS GO_VERSION MICRO_EXIT INTEGRATION_EXIT PARSE_READY" >&2
  exit 2
fi

out=$1
head_sha=$2
run_id=$3
run_attempt=$4
runner_os=$5
go_version=$6
micro_exit=$7
integration_exit=$8
parse_ready=$9

suite_status() {
  if [[ $1 == "0" ]]; then
    echo "success"
  else
    echo "failure"
  fi
}

benchmark_success=false
if [[ $micro_exit == "0" && $integration_exit == "0" ]]; then
  benchmark_success=true
fi

cat > "$out" <<EOF
{
  "schema_version": 1,
  "head_sha": "$head_sha",
  "workflow_run_id": "$run_id",
  "workflow_run_attempt": "$run_attempt",
  "runner_os": "$runner_os",
  "go_version": "$go_version",
  "benchmark_success": $benchmark_success,
  "parse_ready": $parse_ready,
  "normalized_output": "bench-current.json",
  "suites": [
    {
      "name": "micro",
      "status": "$(suite_status "$micro_exit")",
      "exit_code": $micro_exit,
      "output_path": "bench-micro.txt"
    },
    {
      "name": "integration",
      "status": "$(suite_status "$integration_exit")",
      "exit_code": $integration_exit,
      "output_path": "bench-integration.txt"
    }
  ]
}
EOF
