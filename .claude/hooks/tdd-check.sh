#!/bin/bash
# Stop hook: warn if implementation .go files changed without any test files.
# Exits 2 (block + feedback) when tests are missing, 0 otherwise.
#
# Once the agent explains "pure refactor" and the hook accepts, a diff hash
# is saved so the same unchanged diff won't re-trigger on subsequent stops.

ACK_FILE=".claude/.tdd-ack"

# Get modified .go files (staged + unstaged)
changed=$(git diff --name-only HEAD 2>/dev/null; git diff --name-only 2>/dev/null)
go_files=$(echo "$changed" | grep '\.go$' | sort -u)

# Separate implementation files from test files
impl_files=$(echo "$go_files" | grep -v '_test\.go$')
test_files=$(echo "$go_files" | grep '_test\.go$')

# TDD is satisfied when: no impl files changed, or at least one test file changed
if [ -z "$impl_files" ] || [ -n "$test_files" ]; then
    rm -f "$ACK_FILE"
    exit 0
fi

# Hash the current impl-only diff to detect whether it changed since last ack.
diff_content=$(git diff HEAD -- $impl_files 2>/dev/null)
current_hash=$(echo "$diff_content" | md5 -q 2>/dev/null || echo "$diff_content" | md5sum 2>/dev/null | cut -d' ' -f1)
if [ -f "$ACK_FILE" ] && [ "$(cat "$ACK_FILE")" = "$current_hash" ]; then
    exit 0
fi

# Save the hash so the next stop (after the agent explains) won't re-trigger.
echo "$current_hash" > "$ACK_FILE"

# Implementation changed but no tests — warn
echo "TDD check: implementation files changed but no test files were modified:" >&2
echo "$impl_files" | sed 's/^/  /' >&2
echo "" >&2
echo "Write a failing test first, then implement. If this is a pure refactor" >&2
echo "with no behavior change, explain why no test is needed." >&2
exit 2
