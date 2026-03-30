#!/usr/bin/env bash

set -euo pipefail

if [[ -z "${AMUX_PANE:-}" ]]; then
    exit 0
fi

if ! command -v amux >/dev/null 2>&1; then
    exit 0
fi

if ! command -v gh >/dev/null 2>&1; then
    exit 0
fi

if ! command -v jq >/dev/null 2>&1; then
    exit 0
fi

pr_num="${1:-}"
if [[ -z "$pr_num" ]]; then
    pr_num="$(gh pr view --json number --jq .number 2>/dev/null || true)"
fi
if [[ -z "$pr_num" ]]; then
    exit 0
fi

if ! capture="$(amux capture --format json "$AMUX_PANE" 2>/dev/null)"; then
    exit 0
fi

args=("meta" "set" "$AMUX_PANE" "pr=$pr_num")
issue="$(printf '%s\n' "$capture" | jq -r '.meta.kv.issue // empty' 2>/dev/null || true)"
if [[ -n "$issue" ]]; then
    args+=("issue=$issue")
fi

amux "${args[@]}" >/dev/null
