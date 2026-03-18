#!/bin/bash
# PostToolUse hook: after merging a PR, prompt for session postmortem.
# Reads tool input JSON from stdin. Exit 2 sends feedback back to Claude.

input=$(cat)
command=$(echo "$input" | jq -r '.tool_input.command // empty' 2>/dev/null)

if [[ "$command" == gh\ pr\ merge* ]]; then
    echo "PR merged. Run /postmortem now to capture session learnings: What did you learn? Any pain points? Any action items for issues, CLAUDE.md updates, documentation, or hooks?" >&2
    exit 2
fi

exit 0
