#!/bin/bash
# driver-agent-loop.sh — Records a focused agent-loop demo.
#
# Shows: send-keys → wait-busy → wait-idle → capture JSON → react to output.
# Demonstrates the amux agent API workflow that's impossible with tmux polling.
#
# This script is not meant to be run directly — use record.sh with DEMO=agent-loop.

set -euo pipefail

SESSION="agent-loop-$$"
AMUX="amux"
PIDFILE="/tmp/amux-agent-loop-pid-$$"
SIMDIR="/tmp/amux-agent-loop-$$"

cleanup() {
    rm -f "$PIDFILE"
    rm -rf "$SIMDIR"
    pkill -f "amux _server ${SESSION}" 2>/dev/null || true
}
trap cleanup EXIT

write_sim_scripts() {
    mkdir -p "$SIMDIR"

    # Simulated test runner that takes a few seconds and sometimes fails
    cat > "$SIMDIR/test-runner.sh" <<'EOF'
#!/bin/bash
clear
printf "\033[1m$ make test\033[0m\n\n"
sleep 1
printf "Running test suite...\n"
sleep 0.5
printf "  \033[32m✓\033[0m server/handler_test.go (12ms)\n"
sleep 0.3
printf "  \033[32m✓\033[0m server/auth_test.go (8ms)\n"
sleep 0.3
printf "  \033[31m✗\033[0m server/cache_test.go (45ms)\n"
printf "    Expected 200, got 500\n"
sleep 0.3
printf "\n\033[31mFAIL\033[0m  2 passed, 1 failed\n"
printf "$ "
read -r _ 2>/dev/null || sleep 999
EOF

    # Agent script that demonstrates the agent loop
    cat > "$SIMDIR/agent-loop.sh" <<'AGENTEOF'
#!/bin/bash
SESSION="$1"
AMUX="amux"
clear

printf "\033[1;36m── Agent Loop Demo ──\033[0m\n\n"
sleep 1.5

# Step 1: Send command
printf "\033[33m1.\033[0m Sending test command to pane-1...\n"
sleep 0.8
printf "   \033[2m$ amux send-keys pane-1 'make test' Enter\033[0m\n\n"
$AMUX -s "$SESSION" send-keys pane-1 "bash /tmp/amux-agent-loop-$$/test-runner.sh" Enter >/dev/null
sleep 0.5

# Step 2: Wait for busy
printf "\033[33m2.\033[0m Waiting for command to start...\n"
printf "   \033[2m$ amux wait-busy pane-1\033[0m\n"
$AMUX -s "$SESSION" wait-busy pane-1 --timeout 10s >/dev/null
printf "   \033[32m✓\033[0m pane-1 is busy\n\n"
sleep 0.5

# Step 3: Wait for idle
printf "\033[33m3.\033[0m Waiting for command to finish...\n"
printf "   \033[2m$ amux wait-idle pane-1 --timeout 30s\033[0m\n"
$AMUX -s "$SESSION" wait-idle pane-1 --timeout 30s >/dev/null
printf "   \033[32m✓\033[0m pane-1 is idle\n\n"
sleep 0.5

# Step 4: Capture as JSON
printf "\033[33m4.\033[0m Capturing structured output...\n"
printf "   \033[2m$ amux capture --format json pane-1 | jq\033[0m\n\n"
sleep 0.5
output=$($AMUX -s "$SESSION" capture --format json pane-1)
echo "$output" | jq '{name: .name, idle: .idle, last_lines: [.content[-4:][] | select(. != "")]}'
sleep 1

# Step 5: React
printf "\n\033[33m5.\033[0m Checking result...\n"
sleep 0.5
exit_line=$(echo "$output" | jq -r '.content[-2]')
if echo "$exit_line" | grep -q "FAIL"; then
    printf "   \033[31m✗\033[0m Tests failed — agent would diagnose and fix\n"
else
    printf "   \033[32m✓\033[0m Tests passed — agent moves on\n"
fi

printf "\n\033[1;36m── No polling. No regex. Just structured data. ──\033[0m\n"
sleep 5
AGENTEOF

    chmod +x "$SIMDIR"/*.sh
}

# --- Background agent ---
agent() {
    local session="$1"
    local pidfile="$2"
    local uid
    uid="$(id -u)"

    for _ in {1..50}; do
        [ -S "/tmp/amux-${uid}/${session}" ] && break
        sleep 0.2
    done
    if [ ! -S "/tmp/amux-${uid}/${session}" ]; then
        echo "ERROR: amux server socket not found after 10s" >&2
        return 1
    fi

    write_sim_scripts

    sleep 2

    # Create the agent pane
    "$AMUX" -s "$session" spawn --name agent --task "agent loop demo" >/dev/null
    sleep 1

    # Launch test runner shell in pane-1 (stays at prompt)
    "$AMUX" -s "$session" send-keys pane-1 "clear" Enter >/dev/null
    sleep 0.5

    # Launch agent script in agent pane
    "$AMUX" -s "$session" send-keys agent "bash ${SIMDIR}/agent-loop.sh ${session}" Enter >/dev/null

    # Wait for agent script to finish
    sleep 20

    # End recording
    if [ -f "$pidfile" ]; then
        kill "$(cat "$pidfile")" 2>/dev/null || true
    fi
}

echo $$ > "$PIDFILE"
agent "$SESSION" "$PIDFILE" &
exec "$AMUX" -s "$SESSION"
