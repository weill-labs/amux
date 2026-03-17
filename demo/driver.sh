#!/bin/bash
# driver.sh — Runs inside asciinema's PTY to produce the hero recording.
#
# Architecture:
#   1. A background "agent" subprocess drives amux via CLI (Unix socket)
#   2. The foreground amux client renders the TUI into the recorded PTY
#   3. After the demo, the agent kills the client (via saved PID) to end recording
#
# This script is not meant to be run directly — use record.sh instead.

set -euo pipefail

SESSION="hero-$$"
AMUX="amux"
PIDFILE="/tmp/amux-hero-pid-$$"

cleanup() {
    rm -f "$PIDFILE"
    pkill -f "amux _server ${SESSION}" 2>/dev/null || true
}
trap cleanup EXIT

# --- Background agent: drives the demo via CLI ---
agent() {
    local session="$1"
    local pidfile="$2"
    local uid
    uid="$(id -u)"

    # Wait for the amux server socket
    for _ in {1..50}; do
        [ -S "/tmp/amux-${uid}/${session}" ] && break
        sleep 0.2
    done
    if [ ! -S "/tmp/amux-${uid}/${session}" ]; then
        echo "ERROR: amux server socket not found after 10s" >&2
        return 1
    fi
    # Let the TUI render its first frame
    sleep 2

    # Spawn named panes with task metadata
    "$AMUX" -s "$session" spawn --name build --task "LAB-42: run tests" >/dev/null
    sleep 0.8
    "$AMUX" -s "$session" spawn --name lint --task "LAB-43: lint check" >/dev/null
    sleep 1.5

    # Send commands to panes — use clear to hide the raw echo commands,
    # then printf for clean output that looks like real tool output
    "$AMUX" -s "$session" send-keys build \
        'clear && printf "$ make test\n" && sleep 1 && printf "==> Running 42 tests...\n" && sleep 1.5 && printf "✓ All 42 tests passed (3.1s)\n"' Enter >/dev/null
    sleep 0.3
    "$AMUX" -s "$session" send-keys lint \
        'clear && printf "$ eslint src/\n" && sleep 1 && printf "==> Checking 18 files...\n" && sleep 1.5 && printf "✓ No errors found\n"' Enter >/dev/null

    # Wait for both panes to finish (event-based, no polling)
    "$AMUX" -s "$session" wait-idle build --timeout 15s >/dev/null
    "$AMUX" -s "$session" wait-idle lint --timeout 15s >/dev/null

    sleep 0.5

    # Show structured JSON capture in pane-1 (visible in the TUI)
    # Must include -s flag so the capture targets THIS session, not default
    "$AMUX" -s "$session" send-keys pane-1 \
        "clear && amux -s ${session} capture --format json | jq '{session, panes: [.panes[] | {name, task, idle}]}'" Enter >/dev/null

    # Hold final frame so viewer can read the JSON
    sleep 5

    # End recording by killing the TUI client (PID was saved before exec)
    if [ -f "$pidfile" ]; then
        kill "$(cat "$pidfile")" 2>/dev/null || true
    fi
}

# Save our PID — exec will replace this process with amux, keeping the same PID
echo $$ > "$PIDFILE"

# Launch the agent in the background
agent "$SESSION" "$PIDFILE" &

# Replace this process with amux — TUI renders into the recorded PTY
exec "$AMUX" -s "$SESSION"
