#!/usr/bin/env bash
set -euo pipefail

usage() {
    cat <<'EOF'
usage: scripts/batch-delegate.sh <manifest.json>

Dispatch tasks to multiple amux panes from a JSON manifest:
  [{"pane":"pane-47","issue":"LAB-468","task":"Fix black screen"}]

Environment:
  AMUX_BATCH_READY_TIMEOUT   Timeout passed to `amux send-keys --wait ready` (default: 30s)
  AMUX_BATCH_ACCEPT_TIMEOUT  Seconds to wait for output after dispatch (default: 5)
EOF
}

require_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        printf 'missing required command: %s\n' "$1" >&2
        exit 2
    fi
}

trim_trailing_space() {
    local value=$1
    while [[ $value == *" " ]]; do
        value=${value% }
    done
    printf '%s' "$value"
}

validate_manifest() {
    local manifest_path=$1
    jq -e '
        type == "array" and
        all(.[]; (.pane | type == "string" and length > 0) and
                  (.issue | type == "string" and length > 0) and
                  (.task | type == "string" and length > 0))
    ' "$manifest_path" >/dev/null
}

acceptance_error=""
acceptance_dir=""
acceptance_fifo=""
acceptance_err=""
acceptance_pid=""

cleanup_acceptance_stream() {
    exec 3<&- 2>/dev/null || true
    if [[ -n $acceptance_pid ]]; then
        kill "$acceptance_pid" 2>/dev/null || true
        wait "$acceptance_pid" 2>/dev/null || true
    fi
    if [[ -n $acceptance_dir && -d $acceptance_dir ]]; then
        rm -rf "$acceptance_dir"
    fi
    acceptance_dir=""
    acceptance_fifo=""
    acceptance_err=""
    acceptance_pid=""
}

start_acceptance_stream() {
    local pane=$1

    acceptance_dir=$(mktemp -d "${TMPDIR:-/tmp}/batch-delegate.XXXXXX")
    acceptance_fifo=$acceptance_dir/events.fifo
    acceptance_err=$acceptance_dir/events.err
    mkfifo "$acceptance_fifo"

    amux events --filter output,terminal,idle,busy,vt-idle --pane "$pane" --throttle 0s --no-reconnect >"$acceptance_fifo" 2>"$acceptance_err" &
    acceptance_pid=$!
    exec 3<"$acceptance_fifo"
}

acceptance_failure_from_stream() {
    local read_rc=$1

    if kill -0 "$acceptance_pid" 2>/dev/null; then
        acceptance_error="acceptance timeout"
        return 1
    fi

    wait "$acceptance_pid" 2>/dev/null || true
    if [[ -s "$acceptance_err" ]]; then
        acceptance_error=$(tr '\n' ' ' <"$acceptance_err")
        acceptance_error=$(trim_trailing_space "$acceptance_error")
        acceptance_error="acceptance check failed: $acceptance_error"
    elif [[ $read_rc -eq 142 ]]; then
        acceptance_error="acceptance timeout"
    else
        acceptance_error="acceptance wait ended without output"
    fi
    return 1
}

prepare_acceptance_check() {
    local pane=$1
    local timeout=$2
    local line read_rc

    start_acceptance_stream "$pane"
    if IFS= read -r -t "$timeout" line <&3; then
        acceptance_error=""
        return 0
    fi

    read_rc=$?
    acceptance_failure_from_stream "$read_rc"
    cleanup_acceptance_stream
    return 1
}

wait_for_acceptance() {
    local timeout=$1
    local line read_rc

    while IFS= read -r -t "$timeout" line <&3; do
        if [[ $line == *'"type":"output"'* ]]; then
            acceptance_error=""
            cleanup_acceptance_stream
            return 0
        fi
    done

    read_rc=$?
    acceptance_failure_from_stream "$read_rc"
    cleanup_acceptance_stream
    return 1
}

trap cleanup_acceptance_stream EXIT

if [[ ${1:-} == "-h" || ${1:-} == "--help" ]]; then
    usage
    exit 0
fi

if [[ $# -ne 1 ]]; then
    usage >&2
    exit 2
fi

manifest_path=$1
ready_timeout=${AMUX_BATCH_READY_TIMEOUT:-30s}
accept_timeout=${AMUX_BATCH_ACCEPT_TIMEOUT:-5}

require_cmd amux
require_cmd jq

if [[ ! -f $manifest_path ]]; then
    printf 'manifest not found: %s\n' "$manifest_path" >&2
    exit 2
fi

if ! validate_manifest "$manifest_path"; then
    printf 'invalid manifest: %s\n' "$manifest_path" >&2
    exit 2
fi

summary_rows=()
success_count=0
failure_count=0

record_result() {
    local pane=$1
    local issue=$2
    local status=$3
    local detail=$4

    summary_rows+=("${pane}|${issue}|${status}|${detail}")
    if [[ $status == "SUCCESS" ]]; then
        success_count=$((success_count + 1))
    else
        failure_count=$((failure_count + 1))
    fi
}

dispatch_entry() {
    local entry=$1
    local pane issue task

    pane=$(jq -r '.pane' <<<"$entry")
    issue=$(jq -r '.issue' <<<"$entry")
    task=$(jq -r '.task' <<<"$entry")

    if ! amux add-meta "$pane" "issue=$issue" >/dev/null; then
        record_result "$pane" "$issue" "FAILURE" "set issue metadata failed"
        return
    fi

    if ! prepare_acceptance_check "$pane" "$accept_timeout"; then
        record_result "$pane" "$issue" "FAILURE" "$acceptance_error"
        return
    fi

    if ! amux send-keys "$pane" --wait ready --timeout "$ready_timeout" "$task" Enter >/dev/null; then
        cleanup_acceptance_stream
        record_result "$pane" "$issue" "FAILURE" "send-keys failed"
        return
    fi

    if ! wait_for_acceptance "$accept_timeout"; then
        record_result "$pane" "$issue" "FAILURE" "$acceptance_error"
        return
    fi

    record_result "$pane" "$issue" "SUCCESS" "accepted"
}

print_summary() {
    local row pane issue status detail

    printf '%-12s %-10s %-8s %s\n' "PANE" "ISSUE" "STATUS" "DETAIL"
    for row in "${summary_rows[@]}"; do
        IFS='|' read -r pane issue status detail <<<"$row"
        printf '%-12s %-10s %-8s %s\n' "$pane" "$issue" "$status" "$detail"
    done
    printf '\nSuccesses: %d  Failures: %d\n' "$success_count" "$failure_count"
}

while IFS= read -r entry; do
    dispatch_entry "$entry"
done < <(jq -c '.[]' "$manifest_path")

print_summary

if (( failure_count > 0 )); then
    exit 1
fi
