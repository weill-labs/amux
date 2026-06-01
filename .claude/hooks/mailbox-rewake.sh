#!/usr/bin/env bash
set -u

ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
# shellcheck source=../../docs/integrations/amux-mailbox-drain-lib.sh
. "$ROOT/docs/integrations/amux-mailbox-drain-lib.sh"

amux_mailbox_rewake_main "$@"
