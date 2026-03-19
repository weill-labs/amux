#!/bin/bash
# record.sh — Fully automated hero GIF recording. Zero human involvement.
#
# Requires: asciinema, node, ffmpeg (brew install asciinema ffmpeg node)
# First run also installs playwright automatically.
#
# Usage:  bash demo/record.sh                  # hero demo (default)
#         DEMO=agent-loop bash demo/record.sh   # agent loop demo
# Output: demo/hero.gif or demo/agent-loop.gif

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DEMO="${DEMO:-hero}"

case "$DEMO" in
    hero)
        DRIVER="${SCRIPT_DIR}/driver.sh"
        ;;
    agent-loop)
        DRIVER="${SCRIPT_DIR}/driver-agent-loop.sh"
        ;;
    *)
        echo "Unknown demo: $DEMO (supported: hero, agent-loop)"
        exit 1
        ;;
esac

CAST_FILE="${SCRIPT_DIR}/${DEMO}.cast"
GIF_FILE="${SCRIPT_DIR}/${DEMO}.gif"

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
asciinema rec \
    --output-format asciicast-v2 \
    --window-size 160x40 \
    --idle-time-limit 3 \
    --command "bash ${DRIVER}" \
    --overwrite \
    "$CAST_FILE"

echo "Converting to GIF (Playwright + asciinema-player)..."
(cd "$SCRIPT_DIR" && node cast2gif.mjs "$CAST_FILE" "$GIF_FILE" \
    --font "JetBrains Mono" \
    --font-size 12 \
    --fps 8 \
    --scale 2)

# Clean up intermediate file
rm -f "$CAST_FILE"

SIZE=$(du -h "$GIF_FILE" | cut -f1)
echo "Done: ${GIF_FILE} (${SIZE})"
