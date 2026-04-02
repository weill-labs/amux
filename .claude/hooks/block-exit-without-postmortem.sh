#!/bin/bash
# PreToolUse hook: block /exit sent to codex panes without a postmortem.
# Reminds to run postmortem first — session context is destroyed on exit.
#
# Exit 2 = block the tool call and send feedback to Claude.

input=$(cat)
command=$(echo "$input" | jq -r '.tool_input.command // empty' 2>/dev/null)

# Only check amux send-keys/type-keys commands
if ! echo "$command" | grep -qE 'amux (send-keys|type-keys)'; then
    exit 0
fi

# Block /exit commands
if echo "$command" | grep -qE "'/exit'|\"/exit\"|/exit"; then
    echo "BLOCKED: Do not send /exit to codex workers without running a postmortem first. Session context is destroyed on exit and cannot be recovered. Send the postmortem command first, wait for it to complete, then /exit." >&2
    exit 2
fi

exit 0
