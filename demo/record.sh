#!/bin/bash
# record.sh — Fully automated hero GIF recording. Zero human involvement.
#
# Requires: asciinema, agg (brew install asciinema agg)
#
# Usage:  bash demo/record.sh
# Output: demo/hero.gif

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CAST_FILE="${SCRIPT_DIR}/hero.cast"
GIF_FILE="${SCRIPT_DIR}/hero.gif"

# Check dependencies
for cmd in asciinema agg; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "Missing dependency: $cmd"
        echo "Install with: brew install $cmd"
        exit 1
    fi
done

echo "Recording demo..."
asciinema rec \
    --window-size 160x40 \
    --idle-time-limit 3 \
    --command "bash ${SCRIPT_DIR}/driver.sh" \
    --overwrite \
    "$CAST_FILE"

echo "Converting to GIF..."
agg \
    --font-size 16 \
    --font-family "JetBrains Mono,Menlo,monospace" \
    --theme asciinema \
    --speed 1.0 \
    "$CAST_FILE" \
    "$GIF_FILE"

# Clean up intermediate file
rm -f "$CAST_FILE"

SIZE=$(du -h "$GIF_FILE" | cut -f1)
echo "Done: ${GIF_FILE} (${SIZE})"
