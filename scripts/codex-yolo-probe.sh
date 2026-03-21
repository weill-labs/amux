#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/codex-yolo-probe.sh <session> [pane]

Run the manual Codex probe workflow against an existing amux session.
Keep a human client attached in another terminal while this script runs.

Examples:
  SESSION=codex-manual-$(date +%H%M%S)
  amux new "$SESSION"                       # terminal A
  scripts/codex-yolo-probe.sh "$SESSION"   # terminal B

Environment:
  AMUX=amux                amux binary to invoke
  AMUX_PROBE_ALLOW_DEFAULT=1
                           allow probing the default session
EOF
}

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  usage
  exit 0
fi

SESSION=${1:-}
PANE=${2:-pane-1}
AMUX_BIN=${AMUX:-amux}

if [ -z "$SESSION" ]; then
  usage
  exit 1
fi

if [ "$SESSION" = "default" ] && [ "${AMUX_PROBE_ALLOW_DEFAULT:-}" != "1" ]; then
  echo "Refusing to probe the default session."
  echo "Use a fresh named session, or set AMUX_PROBE_ALLOW_DEFAULT=1 to override."
  exit 1
fi

if ! command -v "$AMUX_BIN" >/dev/null 2>&1; then
  echo "Missing amux binary: $AMUX_BIN"
  exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "Missing jq. Install jq to use this probe helper."
  exit 1
fi

step() {
  printf '\n\033[1;36m▸ %s\033[0m\n' "$*"
}

run() {
  printf '$'
  for arg in "$@"; do
    printf ' %q' "$arg"
  done
  printf '\n'
  "$@"
}

warn() {
  printf '\n\033[1;33mwarning:\033[0m %s\n' "$*"
}

note() {
  printf '\n\033[1;34mnote:\033[0m %s\n' "$*"
}

capture_file=$(mktemp "${TMPDIR:-/tmp}/amux-codex-probe.XXXXXX.json")
trap 'rm -f "$capture_file"' EXIT

suspicious=0

capture_state() {
  printf '$ %q -s %q capture --history --format json %q\n' \
    "$AMUX_BIN" "$SESSION" "$PANE"
  "$AMUX_BIN" -s "$SESSION" capture --history --format json "$PANE" >"$capture_file"
  jq '{
    idle,
    current_command,
    child_pids,
    history_tail: (.history[-4:] // []),
    content_tail: (.content[-12:] // [])
  }' "$capture_file"
}

pane_has_live_children() {
  jq -e '(.child_pids | length) > 0' "$capture_file" >/dev/null
}

list_clients() {
  "$AMUX_BIN" -s "$SESSION" list-clients
}

ensure_client_attached() {
  local clients
  clients=$(list_clients)
  printf '%s\n' "$clients"
  if printf '%s\n' "$clients" | grep -q '^No clients attached\.$'; then
    echo
    echo "No client is attached to session '$SESSION'."
    echo "Open 'amux new $SESSION' or 'amux -s $SESSION attach' in another terminal first."
    exit 1
  fi
}

check_visible_capture() {
  local clients output rc
  clients=$(list_clients)
  rc=0
  if ! output=$("$AMUX_BIN" -s "$SESSION" capture "$PANE" 2>&1); then
    rc=$?
  fi
  printf '%s\n' "$output"
  if [ "$rc" -ne 0 ] && ! printf '%s\n' "$clients" | grep -q '^No clients attached\.$'; then
    warn "plain capture failed even though a client is still attached"
    suspicious=1
  fi
}

step "Preflight: require an attached client and capture baseline state"
printf 'Optional third terminal: %s\n' \
  "$AMUX_BIN -s $SESSION events --filter idle,busy,layout --pane $PANE"
ensure_client_attached
capture_state
check_visible_capture

step "Launch codex --yolo and confirm the pane becomes busy"
run "$AMUX_BIN" -s "$SESSION" send-keys "$PANE" "codex --yolo" Enter
if ! run "$AMUX_BIN" -s "$SESSION" wait-busy "$PANE" --timeout 5s; then
  warn "wait-busy timed out after launching codex --yolo"
  suspicious=1
fi
ensure_client_attached
capture_state
check_visible_capture

step "Send /help and inspect the resulting pane state"
run "$AMUX_BIN" -s "$SESSION" send-keys "$PANE" "/help" Enter
sleep 2
capture_state

step "Send Ctrl-C, then compare wait-idle against the JSON child process state"
run "$AMUX_BIN" -s "$SESSION" send-keys "$PANE" C-c
sleep 1
if run "$AMUX_BIN" -s "$SESSION" wait-idle "$PANE" --timeout 3s; then
  capture_state
  if pane_has_live_children; then
    warn "wait-idle returned but the pane still reports child_pids"
    suspicious=1
  fi
else
  capture_state
  if pane_has_live_children; then
    note "Codex is still running after the first Ctrl-C; retrying once"
    run "$AMUX_BIN" -s "$SESSION" send-keys "$PANE" C-c
    sleep 1
    if ! run "$AMUX_BIN" -s "$SESSION" wait-idle "$PANE" --timeout 3s; then
      warn "wait-idle timed out after the second Ctrl-C"
      suspicious=1
    fi
    capture_state
  else
    warn "wait-idle timed out after Ctrl-C even though the pane no longer reports child_pids"
    suspicious=1
  fi
fi
check_visible_capture

if pane_has_live_children; then
  warn "pane still has live children: $(jq -r '.child_pids | join(\",\")' "$capture_file")"
  suspicious=1
fi

if [ "$suspicious" -ne 0 ]; then
  echo
  echo "Probe finished with suspicious results."
  exit 2
fi

echo
echo "Probe finished without detecting contradictions."
