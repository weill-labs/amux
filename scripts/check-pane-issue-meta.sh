#!/usr/bin/env bash

set -u

if [[ -z "${AMUX_PANE:-}" ]]; then
    exit 0
fi

if ! command -v amux >/dev/null 2>&1; then
    exit 0
fi

if ! command -v jq >/dev/null 2>&1; then
    exit 0
fi

if ! capture=$(amux capture --format json "$AMUX_PANE" 2>/dev/null); then
    exit 0
fi

if printf '%s\n' "$capture" | jq -e '.lead == true' >/dev/null 2>&1; then
    exit 0
fi

if ! issue=$(printf '%s\n' "$capture" | jq -r '.meta.kv.issue // empty' 2>/dev/null); then
    exit 0
fi

if [[ -z "$issue" ]]; then
    echo "Pane $AMUX_PANE is missing issue metadata. Start work with \`scripts/set-pane-issue.sh LAB-XXX\` or tag it manually with \`amux meta set \"\$AMUX_PANE\" issue=LAB-XXX\` before declaring work done." >&2
    exit 1
fi

exit 0
