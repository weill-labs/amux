#!/bin/bash
# PostToolUse hook: after PR creation/push, remind about review workflow and check for conflicts.
# Reads tool input JSON from stdin. Exit 2 sends feedback back to Claude.
# Uses `mergeable` field (CONFLICTING/MERGEABLE/UNKNOWN) — not `mergeStateStatus`
# which also reflects CI check status and would false-positive on pending checks.

input=$(cat)
command=$(echo "$input" | jq -r '.tool_input.command // empty' 2>/dev/null)
repo_root=$(git rev-parse --show-toplevel 2>/dev/null)

# Check if the given PR has merge conflicts. GitHub needs a moment after push
# to compute merge state, so we sleep briefly before querying.
check_conflicts() {
    local pr_num="$1"
    sleep 2
    local mergeable
    mergeable=$(gh pr view "$pr_num" --json mergeable --jq .mergeable 2>/dev/null)
    if [[ "$mergeable" == "CONFLICTING" ]]; then
        echo "WARNING: PR #$pr_num has merge conflicts. Rebase onto main and resolve before proceeding." >&2
    fi
}

sync_pane_pr_meta() {
    local pr_num="$1"
    if [[ -n "$repo_root" && -x "$repo_root/scripts/sync-pane-pr-meta.sh" ]]; then
        "$repo_root/scripts/sync-pane-pr-meta.sh" "$pr_num" >/dev/null 2>&1 || true
    fi
}

# After gh pr create: remind to run review workflow + check conflicts
if [[ "$command" == gh\ pr\ create* ]]; then
    pr_num=$(gh pr view --json number --jq .number 2>/dev/null)
    if [[ -n "$pr_num" ]]; then
        sync_pane_pr_meta "$pr_num"
        echo "PR created. Run a review pass and a simplification pass now before considering this done." >&2
        check_conflicts "$pr_num"
        exit 2
    fi
fi

# After git push: remind to run review workflow + check for merge conflicts
if [[ "$command" == git\ push* ]]; then
    pr_num=$(gh pr view --json number --jq .number 2>/dev/null)
    if [[ -n "$pr_num" ]]; then
        sync_pane_pr_meta "$pr_num"
        echo "Pushed to PR #$pr_num. Run a review pass and a simplification pass now." >&2
        check_conflicts "$pr_num"
        exit 2
    fi
fi

exit 0
