#!/bin/bash
# PostToolUse hook: after merging a PR, prompt for session postmortem.
# Reads tool input JSON from stdin. Exit 2 sends feedback back to Claude.

input=$(cat)
command=$(echo "$input" | jq -r '.tool_input.command // empty' 2>/dev/null)

refresh_pane_meta() {
    local repo_root
    if [[ -z "${AMUX_PANE:-}" ]]; then
        return
    fi
    repo_root=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "$repo_root" ]]; then
        return
    fi
    if [[ ! -x "$repo_root/scripts/sync-pane-meta.sh" ]]; then
        return
    fi
    "$repo_root/scripts/sync-pane-meta.sh" "$AMUX_PANE" >/dev/null 2>&1 || true
}

sync_main_after_merge() {
    local repo_root
    repo_root=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "$repo_root" ]]; then
        echo "Cannot auto-sync main after merge: repo root not found." >&2
        return 1
    fi
    if [[ ! -x "$repo_root/scripts/post-merge-main-sync.sh" ]]; then
        echo "Cannot auto-sync main after merge: scripts/post-merge-main-sync.sh is missing or not executable." >&2
        return 1
    fi
    "$repo_root/scripts/post-merge-main-sync.sh"
}

if [[ "$command" == gh\ pr\ merge* ]]; then
    sync_output=$(sync_main_after_merge 2>&1)
    sync_status=$?
    refresh_pane_meta

    if [[ $sync_status -eq 0 ]]; then
        echo "$sync_output" >&2
        echo "PR merged. Run /postmortem now to capture session learnings: What did you learn? Any pain points? Any action items for issues, AGENTS.md or CLAUDE.md updates, documentation, or hooks?" >&2
    else
        echo "PR merged, but post-merge main sync failed: $sync_output" >&2
        echo "Run \`git checkout main && git pull --ff-only\`, then /postmortem." >&2
    fi
    exit 2
fi

exit 0
