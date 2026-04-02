#!/bin/bash
# PreToolUse hook: block kill commands on amux/server processes without
# a reminder to capture diagnostics first (kill -3 for goroutine trace).
# Go runtime handles SIGQUIT (kill -3) by dumping all goroutine stacks.
#
# Exit 2 = block the tool call and send feedback to Claude.

input=$(cat)
command=$(echo "$input" | jq -r '.tool_input.command // empty' 2>/dev/null)

# Only check kill commands
if ! echo "$command" | grep -qE '^kill '; then
    exit 0
fi

# Allow kill -3 (SIGQUIT) — Go runtime dumps goroutine stacks on SIGQUIT
# Also allow kill -6 (SIGABRT) as a fallback diagnostic signal
if echo "$command" | grep -qE 'kill -(3|6|QUIT|ABRT)|kill -s (QUIT|ABRT)'; then
    exit 0
fi

# Block kill on amux processes without diagnostics
if echo "$command" | grep -qiE 'amux|server'; then
    echo "BLOCKED: Before killing amux/server processes, capture diagnostics first. Use 'kill -3 <PID>' (SIGQUIT) to get a Go goroutine trace, then save stderr output. Only kill -9 after diagnostics are captured." >&2
    exit 2
fi

exit 0
