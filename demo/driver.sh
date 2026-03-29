#!/bin/bash
# driver.sh — Runs inside asciinema's PTY to produce the README hero recording.
#
# Architecture:
#   1. A background controller drives amux via the CLI over the Unix socket
#   2. The foreground amux client renders the TUI into the recorded PTY
#   3. The controller ends the recording by killing the foreground client
#
# This script is not meant to be run directly — use record.sh instead.

set -euo pipefail

SESSION="demo-$$"
AMUX="amux"
PIDFILE="/tmp/amux-hero-pid-$$"
SIMDIR="/tmp/amux-demo-$$"

cleanup() {
    rm -f "$PIDFILE"
    rm -rf "$SIMDIR"
    pkill -f "amux _server ${SESSION}" 2>/dev/null || true
}
trap cleanup EXIT

write_sim_scripts() {
    mkdir -p "$SIMDIR"

cat > "$SIMDIR/agent.sh" <<'EOF'
#!/bin/bash
set -euo pipefail
LABEL="$1"
RESPONSE_SCRIPT="$2"
clear

printf "\n"
printf " \033[1magent:\033[0m %s\n" "$LABEL"
printf " \033[2mlocal worker ready for scripted input\033[0m\n"
printf "\n"

printf "\033[1;35m>\033[0m "
read -r prompt

printf "\n"
printf "\033[2m● Working...\033[0m\n\n"
sleep 0.9

bash "$RESPONSE_SCRIPT" "$prompt"

printf "\n\033[32m✓ Returned control to the shell\033[0m\n"
EOF

    cat > "$SIMDIR/resp-fix.sh" <<'EOF'
printf "\033[36m● Read\033[0m(test/type_keys_test.go)\n"
sleep 1.2
printf "\033[36m● Edit\033[0m(demo/driver.sh)\n"
sleep 1.3
printf "\033[36m● Run\033[0m(go test ./test -run TestTypeKeys -count=100)\n\n"
sleep 1.8
printf "  \033[32mPASS\033[0m TestTypeKeysSplit\n"
sleep 1.0
printf "  \033[32mPASS\033[0m TestTypeKeysFocus\n"
sleep 1.0
printf "  \033[32mPASS\033[0m TestTypeKeysCopyMode\n"
sleep 1.0
printf "\n\033[32m✓ Focused slice green; pane is ready for capture\033[0m\n"
EOF

    cat > "$SIMDIR/resp-review.sh" <<'EOF'
printf "\033[36m● Read\033[0m(demo/driver.sh)\n"
sleep 1.2
printf "\033[36m● Read\033[0m(test/type_keys_test.go)\n"
sleep 1.3
printf "\033[36m● Write\033[0m(review.md)\n\n"
sleep 1.5
printf "\033[32m✓ Review summary\033[0m\n"
sleep 1.0
printf "  - human stayed in control while pane-2 ran\n"
sleep 1.0
printf "  - wait idle can unblock without polling loops\n"
sleep 1.1
printf "  - capture JSON can expose cursor + layout state next\n"
EOF

    chmod +x "$SIMDIR"/*.sh
}

run_human() {
    local session="$1"
    local cmd="$2"
    local i
    local ch
    "$AMUX" -s "$session" focus pane-1 >/dev/null
    sleep 0.2
    for ((i = 0; i < ${#cmd}; i++)); do
        ch="${cmd:i:1}"
        "$AMUX" -s "$session" send-keys pane-1 "$ch" >/dev/null
        sleep 0.05
    done
    sleep 1.0
    "$AMUX" -s "$session" send-keys pane-1 Enter >/dev/null
    sleep 1.0
}

run_scripted_prompt() {
    local session="$1"
    local pane="$2"
    local prompt="$3"
    local escaped_prompt
    local cmd

    escaped_prompt="${prompt//\\/\\\\}"
    escaped_prompt="${escaped_prompt//\"/\\\"}"
    cmd="amux send-keys ${pane} --wait ready \"${escaped_prompt}\" Enter && amux wait busy ${pane} --timeout 5s"
    run_human "$session" "$cmd"
}

cycle_focus() {
    local session="$1"
    "$AMUX" -s "$session" type-keys C-a o >/dev/null
}

wait_for_socket() {
    local session="$1"
    local uid
    uid="$(id -u)"
    for _ in {1..50}; do
        [ -S "/tmp/amux-${uid}/${session}" ] && return 0
        sleep 0.2
    done
    echo "ERROR: amux server socket not found after 10s" >&2
    return 1
}

wait_for_idle() {
    local session="$1"
    local pane="$2"
    "$AMUX" -s "$session" wait idle "$pane" --timeout 10s >/dev/null
}

agent() {
    local session="$1"
    local pidfile="$2"
    local layout

    wait_for_socket "$session"
    write_sim_scripts

    sleep 2.5

    layout=$("$AMUX" -s "$session" cursor layout)
    "$AMUX" -s "$session" type-keys C-a '|' >/dev/null
    "$AMUX" -s "$session" wait layout --after "$layout" --timeout 5s >/dev/null
    sleep 1.0

    run_human "$session" 'amux spawn --name review --task "summarize pane-2" && amux move-to review 2'
    sleep 1.8

    "$AMUX" -s "$session" send-keys 2 "bash ${SIMDIR}/agent.sh fix ${SIMDIR}/resp-fix.sh" Enter >/dev/null
    sleep 0.4
    "$AMUX" -s "$session" send-keys review "bash ${SIMDIR}/agent.sh review ${SIMDIR}/resp-review.sh" Enter >/dev/null

    sleep 1.6
    wait_for_idle "$session" 2
    wait_for_idle "$session" review
    sleep 0.8

    run_scripted_prompt "$session" 2 "Patch the demo split flow and rerun the focused tests"
    sleep 1.2
    run_scripted_prompt "$session" review "Watch pane-2 and flag anything risky"
    sleep 1.1

    run_human "$session" 'amux wait idle 2 --timeout 30s && echo "pane-2 idle"'
    sleep 1.0

    cycle_focus "$session"
    sleep 6.5
    cycle_focus "$session"
    sleep 6.5

    "$AMUX" -s "$session" wait idle 2 --timeout 30s >/dev/null
    sleep 0.8

    cycle_focus "$session"
    sleep 1.2
    run_human "$session" "clear && amux capture --format json | jq '.panes[]|{name,idle,cursor,position}'"

    sleep 8

    if [ -f "$pidfile" ]; then
        kill "$(cat "$pidfile")" 2>/dev/null || true
    fi
}

echo $$ > "$PIDFILE"

agent "$SESSION" "$PIDFILE" &

exec "$AMUX" -s "$SESSION"
