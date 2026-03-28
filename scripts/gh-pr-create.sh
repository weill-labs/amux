#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

gh pr create "$@"
"$script_dir/sync-pane-pr-meta.sh" >/dev/null 2>&1 || true
