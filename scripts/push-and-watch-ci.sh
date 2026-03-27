#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

git push "$@"
"$script_dir/watch-pr-ci.sh"
