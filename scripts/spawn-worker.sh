#!/usr/bin/env bash

set -euo pipefail

usage() {
    echo "usage: scripts/spawn-worker.sh --parent <pane> --issue <issue>" >&2
}

die() {
    echo "scripts/spawn-worker.sh: $*" >&2
    exit 2
}

require_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        die "missing required command: $1"
    fi
}

run_quiet() {
    "$@" >/dev/null
}

parent=""
issue=""
amux_bin="${AMUX:-amux}"
git_bin="${GIT:-git}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --parent)
            [[ $# -ge 2 ]] || {
                usage
                exit 2
            }
            parent="$2"
            shift 2
            ;;
        --issue)
            [[ $# -ge 2 ]] || {
                usage
                exit 2
            }
            issue="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            usage
            exit 2
            ;;
    esac
done

if [[ -z "$parent" || -z "$issue" ]]; then
    usage
    exit 2
fi

require_cmd "$amux_bin"
require_cmd "$git_bin"

if ! split_out="$("$amux_bin" split "$parent" --horizontal)"; then
    exit $?
fi

if [[ ! "$split_out" =~ new[[:space:]]+pane[[:space:]]+([^[:space:]]+) ]]; then
    die "failed to parse new pane from: $split_out"
fi
pane="${BASH_REMATCH[1]}"

repo_root="$("$git_bin" rev-parse --show-toplevel)"
repo_name="$(basename "$repo_root")"
issue_slug="${issue,,}"
branch_name="${issue_slug}-${pane}"
worktree_root="$(dirname "$repo_root")"
worktree_path="$worktree_root/${repo_name}-${branch_name}"

run_quiet "$git_bin" worktree add -b "$branch_name" "$worktree_path"

printf -v cd_cmd 'cd %q' "$worktree_path"
run_quiet "$amux_bin" send-keys "$pane" "$cd_cmd" Enter
run_quiet "$amux_bin" send-keys "$pane" "codex --yolo" Enter
run_quiet "$amux_bin" wait vt-idle "$pane"
run_quiet "$amux_bin" send-keys "$pane" Enter
run_quiet "$amux_bin" add-meta "$pane" "issue=$issue"

printf '%s\n' "$pane"
