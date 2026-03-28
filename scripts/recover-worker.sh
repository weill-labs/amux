#!/usr/bin/env bash

set -euo pipefail

usage() {
    cat >&2 <<'EOF'
usage: scripts/recover-worker.sh <pane>

Recover a stuck Codex worker pane by exiting the blocked UI, resuming the
previous session, and confirming pane output advances again.

Example:
  scripts/recover-worker.sh pane-68

Environment:
  AMUX=amux                         amux binary to invoke
  AMUX_RECOVER_VT_IDLE_TIMEOUT=20s  timeout for each vt-idle wait
  AMUX_RECOVER_VT_IDLE_SETTLE=2s    settle window for vt-idle waits
EOF
}

die_usage() {
    usage
    exit 2
}

fail() {
    echo "scripts/recover-worker.sh: $*" >&2
    exit 1
}

require_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "scripts/recover-worker.sh: missing required command: $1" >&2
        exit 2
    fi
}

pane=${1:-}
if [[ $# -ne 1 ]] || [[ "$pane" == "-h" ]] || [[ "$pane" == "--help" ]] || [[ -z "$pane" ]]; then
    if [[ "${1:-}" == "-h" ]] || [[ "${1:-}" == "--help" ]]; then
        usage
        exit 0
    fi
    die_usage
fi

AMUX_BIN=${AMUX:-amux}
VT_IDLE_TIMEOUT=${AMUX_RECOVER_VT_IDLE_TIMEOUT:-20s}
VT_IDLE_SETTLE=${AMUX_RECOVER_VT_IDLE_SETTLE:-2s}

require_cmd "$AMUX_BIN"
require_cmd jq

step() {
    printf '%s\n' "$*"
}

capture_json() {
    "$AMUX_BIN" capture --format json "$pane"
}

wait_vt_idle() {
    "$AMUX_BIN" wait vt-idle "$pane" --settle "$VT_IDLE_SETTLE" --timeout "$VT_IDLE_TIMEOUT" >/dev/null
}

has_child_processes() {
    local capture=$1
    jq -e '(.child_pids // []) | length > 0' <<<"$capture" >/dev/null
}

content_lines() {
    local capture=$1
    jq -r '.content[]? // empty' <<<"$capture"
}

content_snapshot() {
    local capture=$1
    jq -c '.content // []' <<<"$capture"
}

matches_dialog_patterns() {
    local capture=$1
    local content
    content="$(content_lines "$capture")"

    if grep -Fqi "Do you trust the contents of this directory?" <<<"$content" &&
        grep -Fqi "higher risk of prompt injection." <<<"$content"; then
        return 0
    fi

    if grep -Fqi "Press enter to continue" <<<"$content"; then
        return 0
    fi

    if grep -Fqi "Recent sessions:" <<<"$content"; then
        return 0
    fi

    if grep -Fqi "To continue this session, run" <<<"$content"; then
        return 0
    fi

    return 1
}

send_keys() {
    "$AMUX_BIN" send-keys "$pane" "$@" >/dev/null
}

send_and_settle() {
    local description=$1
    shift
    step "$description"
    send_keys "$@"
    wait_vt_idle || fail "$pane did not become vt-idle after: $description"
}

step "Checking whether $pane is stuck"
if ! wait_vt_idle; then
    fail "$pane is not vt-idle; refusing recovery"
fi

initial_capture="$(capture_json)" || fail "failed to capture $pane"
if ! has_child_processes "$initial_capture"; then
    fail "$pane has no child processes; pane does not look stuck"
fi
if ! matches_dialog_patterns "$initial_capture"; then
    fail "$pane does not look stuck; visible content does not match known dialog patterns"
fi

send_and_settle "Sending Escape" Escape
send_and_settle "Sending /exit" /exit Enter
send_and_settle "Launching codex --yolo resume" "codex --yolo resume" Enter
send_and_settle "Selecting the default resume session" Enter

before_continue_capture="$(capture_json)" || fail "failed to capture $pane before continue"
before_continue_content="$(content_snapshot "$before_continue_capture")"

send_and_settle "Sending '.' to continue" . Enter

after_continue_capture="$(capture_json)" || fail "failed to capture $pane after continue"
after_continue_content="$(content_snapshot "$after_continue_capture")"

if [[ "$before_continue_content" == "$after_continue_content" ]]; then
    fail "pane content did not change after resume; output is still stuck"
fi
if matches_dialog_patterns "$after_continue_capture"; then
    fail "pane still matches a blocking dialog after recovery"
fi
if ! has_child_processes "$after_continue_capture"; then
    fail "recovery left $pane without child processes"
fi

step "Recovered $pane"
