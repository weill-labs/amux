#!/usr/bin/env bash

set -euo pipefail

usage() {
    echo "usage: scripts/set-pane-issue.sh [pane] <issue>" >&2
}

if [[ $# -eq 1 ]]; then
    if [[ -z "${AMUX_PANE:-}" ]]; then
        usage
        echo "AMUX_PANE is not set; pass <pane> explicitly when running outside an amux pane." >&2
        exit 1
    fi
    pane="$AMUX_PANE"
    issue="$1"
elif [[ $# -eq 2 ]]; then
    pane="$1"
    issue="$2"
else
    usage
    exit 1
fi

amux meta set "$pane" "issue=$issue"
