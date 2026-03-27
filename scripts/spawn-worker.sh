#!/usr/bin/env bash
# Spawn a new codex worker pane for a task.
#
# Usage:
#   scripts/spawn-worker.sh <parent-pane> <issue> <prompt>
#
# Example:
#   scripts/spawn-worker.sh pane-105 LAB-505 "Make Pane.Close() non-blocking by design"
#
# What it does:
#   1. Finds a free worktree (clean, on main, not used by any pane)
#   2. Pulls latest origin/main in that worktree
#   3. Splits a new pane under the parent
#   4. Tags the pane with issue metadata
#   5. cd's to the worktree and starts codex --yolo
#   6. Handles the codex trust dialog
#   7. Sends the prompt
#   8. Confirms codex accepted
set -euo pipefail

PARENT="${1:?Usage: spawn-worker.sh <parent-pane> <issue> <prompt>}"
ISSUE="${2:?Usage: spawn-worker.sh <parent-pane> <issue> <prompt>}"
PROMPT="${3:?Usage: spawn-worker.sh <parent-pane> <issue> <prompt>}"

# 1. Find a free worktree
WORKTREE=""
USED_DIRS=$(amux list 2>/dev/null | awk 'NR>1 {print $5}' | sed 's|~|'"$HOME"'|')
for dir in "$HOME"/sync/github/amux/amux*/; do
  [ -d "$dir/.git" ] || [ -f "$dir/.git" ] || continue
  branch=$(git -C "$dir" branch --show-current 2>/dev/null) || continue
  dirty=$(git -C "$dir" status --porcelain 2>/dev/null | wc -l | tr -d ' ')
  if [ "$branch" = "main" ] && [ "$dirty" -eq 0 ]; then
    resolved=$(cd "$dir" && pwd -P)
    if ! echo "$USED_DIRS" | grep -qF "$resolved" 2>/dev/null; then
      WORKTREE="$dir"
      break
    fi
  fi
done

if [ -z "$WORKTREE" ]; then
  echo "ERROR: No free worktree found (clean, on main, not used by any pane)" >&2
  exit 1
fi
echo "Using worktree: $WORKTREE"

# 2. Pull latest
git -C "$WORKTREE" pull --ff-only >/dev/null 2>&1 || true

# 3. Split new pane
SPLIT_OUTPUT=$(amux split "$PARENT" --horizontal)
NEW_PANE=$(echo "$SPLIT_OUTPUT" | grep -oE 'pane-[0-9]+' | tail -1)
NEW_ID=$(echo "$NEW_PANE" | grep -oE '[0-9]+')
echo "Created $NEW_PANE"

# 4. Tag with issue metadata
amux add-meta "$NEW_PANE" issue="$ISSUE"

# 5. cd and start codex in one shell command
amux send-keys "$NEW_PANE" "cd $WORKTREE && codex --yolo" Enter

# 6. Handle trust dialog — poll full-session capture for the pane content
echo "Waiting for codex to start..."
for i in $(seq 1 30); do
  sleep 1
  CONTENT=$(amux capture --format json 2>/dev/null | python3 -c "
import sys, json
d = json.load(sys.stdin)
for p in d.get('panes', []):
    if p['id'] == $NEW_ID:
        lines = [l.strip() for l in p.get('content', []) if l.strip()]
        print('\n'.join(lines[-5:]))
" 2>/dev/null || true)

  if echo "$CONTENT" | grep -qi "trust\|Press enter to continue"; then
    amux send-keys "$NEW_PANE" Enter
    echo "Accepted trust dialog"
    continue
  fi

  if echo "$CONTENT" | grep -qi "gpt"; then
    echo "Codex ready"
    break
  fi
done

# 7. Send the prompt
FULL_PROMPT="Fix $ISSUE: $PROMPT. Branch from main, open a PR with Closes $ISSUE."
amux send-keys "$NEW_PANE" "$FULL_PROMPT" Enter
echo "Sent task to $NEW_PANE"

# 8. Confirm accepted
if amux wait content "$NEW_PANE" "Working" --timeout 15s 2>/dev/null; then
  echo "ACCEPTED: $NEW_PANE is working on $ISSUE in $(basename "$WORKTREE")"
else
  echo "WARNING: $NEW_PANE did not confirm acceptance — check manually"
fi
