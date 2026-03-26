#!/bin/bash
# PostToolUse hook: after merging a PR, prompt for session postmortem.
# Reads tool input JSON from stdin. Exit 2 sends feedback back to Claude.

input=$(cat)
command=$(echo "$input" | jq -r '.tool_input.command // empty' 2>/dev/null)

refresh_pane_meta() {
    if [[ -z "${AMUX_PANE:-}" ]]; then
        return
    fi
    if ! command -v amux >/dev/null 2>&1; then
        return
    fi
    amux refresh-meta "$AMUX_PANE" >/dev/null 2>&1 || true
}

if [[ "$command" == gh\ pr\ merge* ]]; then
    refresh_pane_meta
    echo "PR merged. Run /postmortem now to capture session learnings: What did you learn? Any pain points? Any action items for issues, AGENTS.md or CLAUDE.md updates, documentation, or hooks?" >&2
    exit 2
fi

exit 0
