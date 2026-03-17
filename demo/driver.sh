#!/bin/bash
# driver.sh — Runs inside asciinema's PTY to produce the hero recording.
#
# Architecture:
#   1. A background "agent" subprocess drives amux via CLI (Unix socket)
#   2. The foreground amux client renders the TUI into the recorded PTY
#   3. After the demo, the agent kills the client (via saved PID) to end recording
#
# The demo simulates Claude Code sessions for reproducible recording.
# This script is not meant to be run directly — use record.sh instead.

set -euo pipefail

SESSION="hero-$$"
AMUX="amux"
PIDFILE="/tmp/amux-hero-pid-$$"
SIMDIR="/tmp/amux-demo-$$"

cleanup() {
    rm -f "$PIDFILE"
    rm -rf "$SIMDIR"
    pkill -f "amux _server ${SESSION}" 2>/dev/null || true
}
trap cleanup EXIT

# Write simulation scripts to temp files so panes show clean output
# (send-keys would otherwise echo the raw printf/sleep pipeline).
write_sim_scripts() {
    mkdir -p "$SIMDIR"

    cat > "$SIMDIR/server.sh" <<'EOF'
clear
printf "\033[1m$ claude \"create an Express API server\"\033[0m\n\n"
sleep 0.8
printf "● Reading project structure...\n\n"
sleep 1
printf "\033[36m⏺ Write\033[0m(package.json)\n"
sleep 0.4
printf "\033[36m⏺ Write\033[0m(src/server.js)\n"
sleep 0.4
printf "\033[36m⏺ Write\033[0m(src/routes.js)\n"
sleep 0.6
printf "\033[36m⏺ Run\033[0m(npm install)\n"
sleep 1.2
printf "\n\033[32m✓ Created 3 files, installed deps\033[0m\n"
EOF

    cat > "$SIMDIR/tests.sh" <<'EOF'
clear
printf "\033[1m$ claude \"add unit tests\"\033[0m\n\n"
sleep 1
printf "● Analyzing src/server.js...\n\n"
sleep 0.8
printf "\033[36m⏺ Write\033[0m(test/server.test.js)\n"
sleep 0.5
printf "\033[36m⏺ Run\033[0m(npm test)\n\n"
sleep 1.5
printf "  \033[32mPASS\033[0m test/server.test.js\n"
printf "    \033[32m✓\033[0m health check (3ms)\n"
printf "    \033[32m✓\033[0m POST /api/run (8ms)\n"
printf "    \033[32m✓\033[0m error handling (2ms)\n"
sleep 0.5
printf "\n\033[32m✓ 3 tests passing\033[0m\n"
EOF

    cat > "$SIMDIR/review.sh" <<'EOF'
clear
printf "\033[1m$ claude \"review for security issues\"\033[0m\n\n"
sleep 0.8
printf "● Scanning codebase...\n\n"
sleep 1.2
printf "\033[33m⚠ Found 1 issue:\033[0m\n"
printf "  src/server.js:12\n"
printf "  Missing rate limiting on /api\n\n"
sleep 0.8
printf "\033[36m⏺ Edit\033[0m(src/server.js)\n"
printf "  + rate-limit middleware\n"
sleep 0.6
printf "\n\033[32m✓ Fixed 1 issue\033[0m\n"
EOF

    cat > "$SIMDIR/devlog.sh" <<'EOF'
clear
printf "\033[1m$ tail -f dev-server.log\033[0m\n\n"
sleep 1.5
printf "\033[90m21:14:01\033[0m \033[32mINFO\033[0m  server started on :3000\n"
sleep 1.5
printf "\033[90m21:14:03\033[0m \033[32mINFO\033[0m  GET /health \033[32m200\033[0m 4ms\n"
sleep 1.5
printf "\033[90m21:14:05\033[0m \033[32mINFO\033[0m  POST /api/run \033[32m201\033[0m 38ms\n"
sleep 2
printf "\033[90m21:14:07\033[0m \033[34mDEBG\033[0m  agent connected ws://localhost:3000\n"
EOF

    chmod +x "$SIMDIR"/*.sh
}

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

    write_sim_scripts

    # Let the TUI render its first frame
    sleep 2

    # Spawn a claude pane for tests
    "$AMUX" -s "$session" spawn --name tests --task "add unit tests" >/dev/null
    sleep 1

    # Vertical split on pane-1 to create review pane below it
    "$AMUX" -s "$session" focus pane-1 >/dev/null
    sleep 0.3
    local review_pane
    review_pane=$("$AMUX" -s "$session" split v | awk '{print $NF}')
    sleep 1

    # Root horizontal split for the utility pane (rightmost)
    "$AMUX" -s "$session" focus pane-1 >/dev/null
    sleep 0.3
    local util_pane
    util_pane=$("$AMUX" -s "$session" split root | awk '{print $NF}')
    sleep 1

    # Run sim scripts in each pane — only "bash /path" is typed, then clear hides it
    "$AMUX" -s "$session" send-keys pane-1 "bash ${SIMDIR}/server.sh" Enter >/dev/null
    sleep 0.3
    "$AMUX" -s "$session" send-keys tests "bash ${SIMDIR}/tests.sh" Enter >/dev/null
    sleep 0.3
    "$AMUX" -s "$session" send-keys "$review_pane" "bash ${SIMDIR}/review.sh" Enter >/dev/null
    sleep 0.3
    "$AMUX" -s "$session" send-keys "$util_pane" "bash ${SIMDIR}/devlog.sh" Enter >/dev/null

    # Wait for claude panes to finish
    "$AMUX" -s "$session" wait-idle pane-1 --timeout 20s >/dev/null
    "$AMUX" -s "$session" wait-idle tests --timeout 20s >/dev/null
    "$AMUX" -s "$session" wait-idle "$review_pane" --timeout 20s >/dev/null
    sleep 0.5

    # Minimize the review pane — reclaims vertical space
    "$AMUX" -s "$session" minimize "$review_pane" >/dev/null
    sleep 1.5

    # Show structured JSON capture in pane-1
    "$AMUX" -s "$session" send-keys pane-1 \
        "clear && amux -s ${session} capture --format json | jq '{session, panes: [.panes[] | {name, task, idle, minimized}]}'" Enter >/dev/null

    # Hold final frame so viewer can read the JSON
    sleep 5

    # End recording by killing the TUI client
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
