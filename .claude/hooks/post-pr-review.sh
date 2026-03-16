#!/bin/bash
# PostToolUse hook: after `gh pr create`, remind Claude to run review agents.
# Reads tool input JSON from stdin, checks if command was gh pr create.
# Exit 2 sends feedback back to Claude.

input=$(cat)
command=$(echo "$input" | jq -r '.tool_input.command // empty' 2>/dev/null)

if [[ "$command" == gh\ pr\ create* ]]; then
    echo "PR created. Run the code-reviewer and code-simplifier agents now to review the changes before considering this done." >&2
    exit 2
fi

exit 0
