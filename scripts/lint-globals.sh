#!/usr/bin/env bash
# Detect mutable package-level vars used as test seams.
# Scans only staged content when STAGED_ONLY=1 (set by the pre-commit hook).
# Otherwise scans the full working tree (CI / manual runs).
set -euo pipefail

# Known globals that are actively being cleaned up (LAB-412).
# Remove entries as they get refactored into struct fields or parameters.
ALLOWLIST=(
  "sessionName"                      # main.go — set once during arg parsing, not a test seam
  "BuildCommit"                      # main.go — set via ldflags
  "BuildVersion"                     # server/server.go — set via ldflags
  "activePointCounter"               # mux/window.go — atomic counter, not a test seam
  "source"                           # terminfo/terminfo.go — embedded data
  "AllEvents"                        # hooks/hooks.go — read-only registry
  "hexColorCache"                    # render/compositor.go — read-only after init, pre-computed palette
)

allowlist_pattern=$(printf "|%s" "${ALLOWLIST[@]}")
allowlist_pattern="${allowlist_pattern:1}"  # strip leading |

# Choose source: staged content (pre-commit) or working tree (CI / manual)
if [ "${STAGED_ONLY:-}" = "1" ]; then
  source_content=$(git diff --cached --diff-filter=ACM -U0 -- '*.go' \
    ':!third_party/' ':!vendor/' ':!*_test.go' | grep -E '^\+' | grep -vF '+++' || true)
else
  source_content=$(grep -rn '^var [a-zA-Z]' --include='*.go' \
    --exclude-dir=third_party --exclude-dir=vendor \
    --exclude='*_test.go' \
    . 2>/dev/null || true)
fi

# No content to check — exit clean
if [ -z "$source_content" ]; then
  exit 0
fi

# Match package-level var declarations (single-line only — grouped var blocks
# would need multi-line parsing which is impractical in bash).
violations=$(
  echo "$source_content" |
  grep -E '^\+?\s*var [a-zA-Z]' |
  grep -v '^\+?\s*//' |
  # Exclude const-like patterns: sync types, embed, error sentinels, byte slices, maps, string constants, registries, arrays
  grep -v 'sync\.\|embed\.\|= errors\.New\|= fmt\.Errorf\|= \[\]byte\|= map\[' |
  grep -v '= \[\]string{\|= "\|= \[\.\.\.]\|= \[' |
  # Exclude the allowlist
  grep -vE "(${allowlist_pattern})(\s|=)" || true
)

if [ -n "$violations" ]; then
  echo "ERROR: New mutable package-level vars detected."
  echo "Inject dependencies via struct fields or function parameters instead."
  echo "See CLAUDE.md 'Inject dependencies' and PR #388 for the pattern."
  echo ""
  echo "$violations"
  exit 1
fi
