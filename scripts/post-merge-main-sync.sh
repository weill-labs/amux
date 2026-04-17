#!/bin/bash
set -euo pipefail

stash_message="auto-stash by post-merge-main-sync"
ignored_tracked_paths=""
stashed_benign_changes=0

is_ignored_tracked_path() {
    local path="$1"

    [[ -n "$ignored_tracked_paths" ]] || return 1
    grep -Fqx -- "$path" <<<"$ignored_tracked_paths"
}

status_output=$(git status --porcelain --untracked-files=all)
if [[ -n "$status_output" ]]; then
    ignored_tracked_paths=$(git ls-files -ci --exclude-standard)
    staged_changes=()
    non_benign_changes=()

    while IFS= read -r line; do
        [[ -n "$line" ]] || continue

        index_status=${line:0:1}
        worktree_status=${line:1:1}
        path=${line:3}

        if [[ "$index_status" != " " && "$index_status" != "?" ]]; then
            staged_changes+=("$line")
            continue
        fi

        if [[ "$index_status$worktree_status" == "??" ]]; then
            continue
        fi

        if is_ignored_tracked_path "$path"; then
            continue
        fi

        non_benign_changes+=("$line")
    done <<<"$status_output"

    if [[ ${#staged_changes[@]} -gt 0 ]]; then
        echo "Cannot auto-sync main after merge: worktree has staged changes. Leave staged changes alone and sync main manually after you commit or unstage them." >&2
        printf '  %s\n' "${staged_changes[@]}" >&2
        exit 1
    fi

    if [[ ${#non_benign_changes[@]} -gt 0 ]]; then
        echo "Cannot auto-sync main after merge: worktree has non-benign unstaged changes." >&2
        echo "Commit, stash, or discard them first:" >&2
        printf '  %s\n' "${non_benign_changes[@]}" >&2
        exit 1
    fi

    git stash push -u -m "$stash_message" >/dev/null
    stashed_benign_changes=1
fi

current_branch=$(git branch --show-current 2>/dev/null || true)
if [[ "$current_branch" != "main" ]]; then
    if ! checkout_output=$(git checkout main 2>&1); then
        echo "$checkout_output" >&2
        exit 1
    fi
    checked_out_main=1
else
    checked_out_main=0
fi

if ! pull_output=$(git pull --ff-only 2>&1); then
    echo "$pull_output" >&2
    exit 1
fi

if [[ "$checked_out_main" -eq 1 ]]; then
    sync_message="Checked out main and pulled latest origin/main."
else
    sync_message="Pulled latest origin/main on main."
fi

if [[ "$stashed_benign_changes" -eq 0 ]]; then
    echo "$sync_message"
    exit 0
fi

if stash_pop_output=$(git stash pop 2>&1); then
    echo "$sync_message Restored auto-stashed benign changes."
    exit 0
fi

echo "$sync_message"
echo "Restoring auto-stashed benign changes hit conflicts; stash entry was kept for manual recovery."
echo "$stash_pop_output"
