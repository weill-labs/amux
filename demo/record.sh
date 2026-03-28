#!/bin/bash
# record.sh — Fully automated README GIF recording.
#
# Requires: asciinema, node, ffmpeg (brew install asciinema ffmpeg node)
# First run also installs playwright automatically.
#
# Usage:  bash demo/record.sh
# Output: demo/hero.gif

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CAST_FILE="${SCRIPT_DIR}/hero.cast"
GIF_FILE="${SCRIPT_DIR}/hero.gif"

# Check dependencies
for cmd in amux asciinema node ffmpeg jq; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "Missing dependency: $cmd"
        echo "Install with: brew install $cmd"
        exit 1
    fi
done

# Install playwright if needed
if [ ! -d "${SCRIPT_DIR}/node_modules/playwright" ]; then
    echo "Installing playwright..."
    (cd "$SCRIPT_DIR" && npm install && npx playwright install chromium)
fi

echo "Recording demo..."
record_args=(
    --window-size 160x40
    --idle-time-limit 5
    --command "bash ${SCRIPT_DIR}/driver.sh"
    --overwrite
    "$CAST_FILE"
)

# asciinema 2.x records v2 by default but doesn't recognize --output-format.
if asciinema rec --help 2>&1 | grep -q -- '--output-format'; then
    record_args=(--output-format asciicast-v2 "${record_args[@]}")
fi

asciinema rec "${record_args[@]}"

echo "Converting to GIF (Playwright + asciinema-player)..."
(cd "$SCRIPT_DIR" && node cast2gif.mjs "$CAST_FILE" "$GIF_FILE" \
    --font "Menlo" \
    --font-size 14 \
    --fps 7 \
    --scale 2)

# Clean up intermediate file
rm -f "$CAST_FILE"

SIZE=$(du -h "$GIF_FILE" | cut -f1)
echo "Done: ${GIF_FILE} (${SIZE})"
