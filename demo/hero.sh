#!/bin/bash
# hero.sh — Scripted demo of amux's agent-native workflow.
#
# Drives amux via CLI to show:
#   1. Programmatic pane creation with names and tasks
#   2. Sending commands to panes
#   3. Structured JSON capture with idle/busy status
#   4. Event-based wait-idle (no polling)
#
# Usage:
#   1. Start amux in another terminal:  amux new demo
#   2. Run this script:                 bash demo/hero.sh
#   3. Watch the amux TUI while the script runs
#
# For recording: screen-record the amux TUI window while this script
# executes in a separate terminal.

set -euo pipefail

SESSION="demo"
AMUX="amux"

# --- Helpers ---

step() {
    printf '\n\033[1;36m▸ %s\033[0m\n' "$*"
    sleep 0.8
}

# --- Cleanup ---

cleanup() {
    "$AMUX" -s "$SESSION" kill build 2>/dev/null || true
    "$AMUX" -s "$SESSION" kill lint  2>/dev/null || true
    "$AMUX" -s "$SESSION" kill logs  2>/dev/null || true
}
trap cleanup EXIT

# --- Demo ---

step "Spawning panes..."

"$AMUX" -s "$SESSION" spawn --name build --task "LAB-42: run tests"
sleep 0.3
"$AMUX" -s "$SESSION" spawn --name lint  --task "LAB-43: lint check"
sleep 0.3
"$AMUX" -s "$SESSION" spawn --name logs  --task "LAB-44: tail logs"
sleep 1

step "Sending commands to panes..."

"$AMUX" -s "$SESSION" send-keys build \
    'echo "$ make test" && sleep 1 && echo "==> Running 42 tests..." && sleep 2 && echo "✓ All 42 tests passed (3.1s)"' Enter
sleep 0.3

"$AMUX" -s "$SESSION" send-keys lint \
    'echo "$ eslint src/" && sleep 1.5 && echo "==> Checking 18 files..." && sleep 1.5 && echo "✓ No errors found"' Enter
sleep 0.3

"$AMUX" -s "$SESSION" send-keys logs \
    'echo "$ tail -f /var/log/app.log" && sleep 0.5 && echo "[INFO] server started on :8080" && sleep 1 && echo "[INFO] request GET /health 200 12ms" && sleep 1 && echo "[INFO] request POST /api/run 201 45ms"' Enter

step "Waiting for build to finish (event-based, no polling)..."

"$AMUX" -s "$SESSION" wait-idle build --timeout 15s

step "Build done. Querying structured state..."

"$AMUX" -s "$SESSION" capture --format json | jq '{
    session,
    panes: [.panes[] | {name, task, idle, current_command}]
}'

sleep 3

step "Demo complete."
