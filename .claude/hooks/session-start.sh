#!/bin/bash
# SessionStart hook: ensure git hooks are configured.
# Runs `make setup` if core.hooksPath is not set to .githooks.

hooks_path=$(git config core.hooksPath 2>/dev/null)
if [[ "$hooks_path" != ".githooks" ]]; then
    make setup >&2 2>&1
fi

exit 0
