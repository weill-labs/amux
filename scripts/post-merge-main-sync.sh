#!/bin/bash
set -euo pipefail

if ! git diff --quiet --ignore-submodules --; then
    echo "Cannot auto-sync main after merge: worktree has unstaged changes." >&2
    exit 1
fi

if ! git diff --cached --quiet --ignore-submodules --; then
    echo "Cannot auto-sync main after merge: worktree has staged changes." >&2
    exit 1
fi

if [[ -n "$(git ls-files --others --exclude-standard)" ]]; then
    echo "Cannot auto-sync main after merge: worktree has untracked files." >&2
    exit 1
fi

current_branch=$(git branch --show-current 2>/dev/null || true)
if [[ "$current_branch" != "main" ]]; then
    git checkout main >/dev/null
    checked_out_main=1
else
    checked_out_main=0
fi

git pull --ff-only >/dev/null

if [[ "$checked_out_main" -eq 1 ]]; then
    echo "Checked out main and pulled latest origin/main."
else
    echo "Pulled latest origin/main on main."
fi
