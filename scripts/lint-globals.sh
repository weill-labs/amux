#!/usr/bin/env bash
# Detect mutable package-level vars used as test seams.
# Allowed: consts, types, sync.Once, embed, and the known allowlist below.
set -euo pipefail

# Known globals that are actively being cleaned up (LAB-412).
# Remove entries as they get refactored into struct fields or parameters.
ALLOWLIST=(
  "sessionName"                      # main.go — set once during arg parsing
  "BuildCommit"                      # main.go — set via ldflags
  "copyToClipboard"                  # client/attach.go — LAB-412
  "clipboardStdout"                  # client/clipboard.go — LAB-412
  "runClipboardCommand"              # client/clipboard.go — LAB-412
  "termGetSize"                      # client/attach.go — LAB-412
  "attachBootstrapCorrectionWindow"  # client/attach.go — LAB-412
  "renderFrameInterval"              # client/client.go — LAB-412
  "renderPriorityWindow"             # client/client.go — LAB-412
  "copyToClipboard"                  # client/attach.go — LAB-412
  "resolveServerReloadExecPath"      # server/commands_remote.go — LAB-412
  "defaultVTIdleSettle"              # server/vt_idle.go — LAB-412
  "defaultUndoGracePeriod"           # server/session_pane.go — LAB-412
  "timeNow"                          # render/screen.go — LAB-416
  "debugDefault"                     # render/compositor.go — LAB-416
  "activePointCounter"               # mux/window.go — atomic counter, not a test seam
  "BuildVersion"                     # server/server.go — set via ldflags
  "source"                           # terminfo/terminfo.go — embedded data
  "AllEvents"                        # hooks/hooks.go — read-only registry
)

allowlist_pattern=$(printf "|%s" "${ALLOWLIST[@]}")
allowlist_pattern="${allowlist_pattern:1}"  # strip leading |

# Find mutable package-level vars in first-party Go files (exclude third_party, vendor, test files)
violations=$(
  grep -rn '^var [a-zA-Z]' --include='*.go' \
    --exclude-dir=third_party --exclude-dir=vendor \
    --exclude='*_test.go' \
    . |
  # Exclude const-like patterns: sync types, embed, error sentinels, byte slices, maps, string constants, registries
  grep -v 'sync\.\|embed\.\|= errors\.New\|= fmt\.Errorf\|= \[\]byte\|= map\[' |
  grep -v '= \[\]string{\|= "' |
  # Exclude the allowlist
  grep -vE "var (${allowlist_pattern}) " || true
)

if [ -n "$violations" ]; then
  echo "ERROR: New mutable package-level vars detected."
  echo "Inject dependencies via struct fields or function parameters instead."
  echo "See CLAUDE.md 'Inject dependencies' and PR #388 for the pattern."
  echo ""
  echo "$violations"
  exit 1
fi
