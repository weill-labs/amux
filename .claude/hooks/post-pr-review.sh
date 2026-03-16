#!/bin/bash
# PostToolUse hook: after PR creation/push, run review agents and check for conflicts.
# Reads tool input JSON from stdin. Exit 2 sends feedback back to Claude.
# Uses `mergeable` field (CONFLICTING/MERGEABLE/UNKNOWN) — not `mergeStateStatus`
# which also reflects CI check status and would false-positive on pending checks.

input=$(cat)
command=$(echo "$input" | jq -r '.tool_input.command // empty' 2>/dev/null)

check_conflicts() {
    local pr_num
    pr_num=$(gh pr view --json number --jq .number 2>/dev/null)
    if [[ -z "$pr_num" ]]; then
        return
    fi
    sleep 2
    local mergeable
    mergeable=$(gh pr view "$pr_num" --json mergeable --jq .mergeable 2>/dev/null)
    if [[ "$mergeable" == "CONFLICTING" ]]; then
        echo "WARNING: PR #$pr_num has merge conflicts. Rebase onto main and resolve before proceeding." >&2
    fi
}

# After gh pr create: remind to run review agents + check conflicts
if [[ "$command" == gh\ pr\ create* ]]; then
    echo "PR created. Run the code-reviewer and code-simplifier agents now to review the changes before considering this done." >&2
    check_conflicts
    exit 2
fi

# After git push: check for merge conflicts on the current PR
if [[ "$command" == git\ push* ]]; then
    check_conflicts
fi

exit 0
