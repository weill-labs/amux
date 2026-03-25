#!/usr/bin/env bash
# Detect new sync.Mutex, sync.RWMutex, or sync.Map usage in Go files.
# These indicate shared mutable state — use the actor model (channel-based
# event loop) instead. See CLAUDE.md 'Inject dependencies' and PR #403.
#
# Scans only staged content when STAGED_ONLY=1 (set by the pre-commit hook).
# Otherwise scans the full working tree (CI / manual runs).
set -euo pipefail

# Known usages being tracked for actor conversion.
# Remove entries as they get refactored.
ALLOWLIST=(
  # Production — tracked for conversion
  "hooks.go"                           # hooks.Registry RWMutex — LAB-427
  "emulator.go"                        # vtEmulator.scrollbackMu — LAB-432
  "client_conn.go"                     # clientConn.disconnectReasonMu — small, low priority

  # Third-party — not our code
  "safe_emulator.go"                   # charmbracelet/x/vt SafeEmulator

  # Test helpers — coordination between test goroutines
  "host_conn_test.go"                  # remote host connection test mocks
  "event_helpers_test.go"              # event subscriber test helper
  "test_sshd_test.go"                  # test SSH server size tracking
  "pane_output_recorder_test.go"       # test pane output accumulator
  "attach_session_test.go"             # test session/conn mocks
  "clock_test.go"                      # fakeClock and fakeTimer
  "wait_ready_test.go"                 # test wait-ready helpers
  "hooks_test.go"                      # hook test recorder
  "pty_client_harness_test.go"         # PTY client test harness
  "capture_forward_test.go"            # capture test state tracking
  "amux_harness_test.go"              # nested harness startup serialization
  "daemon_test.go"                     # daemon test state tracking
)

allowlist_pattern=$(printf "|%s" "${ALLOWLIST[@]}")
allowlist_pattern="${allowlist_pattern:1}"  # strip leading |

# Choose source: staged content (pre-commit) or working tree (CI / manual)
if [ "${STAGED_ONLY:-}" = "1" ]; then
  source_content=$(git diff --cached --diff-filter=ACM -U0 -- '*.go' \
    ':!vendor/' | grep -E '^\+' | grep -vF '+++' || true)
else
  source_content=$(grep -rn 'sync\.\(Mutex\|RWMutex\|Map\)\b' --include='*.go' \
    --exclude-dir=vendor \
    . 2>/dev/null || true)
fi

# No content to check — exit clean
if [ -z "$source_content" ]; then
  exit 0
fi

# Match sync.Mutex, sync.RWMutex, sync.Map declarations and field types
violations=$(
  echo "$source_content" |
  grep -E 'sync\.(Mutex|RWMutex|Map)\b' |
  grep -v '^\+?\s*//' |
  # Exclude allowlisted files
  grep -vE "(${allowlist_pattern})" || true
)

if [ -n "$violations" ]; then
  echo "ERROR: New sync.Mutex/RWMutex/Map usage detected."
  echo "Use the actor model (channel-based event loop) instead."
  echo "See CLAUDE.md and PR #403 for the pattern."
  echo ""
  echo "$violations"
  echo ""
  echo "If this is a legitimate exception, add the filename to scripts/lint-sync.sh ALLOWLIST."
  exit 1
fi
