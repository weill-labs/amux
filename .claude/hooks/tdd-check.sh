#!/bin/bash
# Stop hook: warn if implementation .go files changed without any test files.
# Exits 2 (block + feedback) when tests are missing, 0 otherwise.

# Get modified .go files (staged + unstaged)
changed=$(git diff --name-only HEAD 2>/dev/null; git diff --name-only 2>/dev/null)

if [ -z "$changed" ]; then
    exit 0
fi

# Filter to .go files only
go_files=$(echo "$changed" | grep '\.go$' | sort -u)
if [ -z "$go_files" ]; then
    exit 0
fi

# Separate implementation files from test files
impl_files=$(echo "$go_files" | grep -v '_test\.go$')
test_files=$(echo "$go_files" | grep '_test\.go$')

# If no implementation files changed, nothing to check
if [ -z "$impl_files" ]; then
    exit 0
fi

# If at least one test file was also modified, TDD is satisfied
if [ -n "$test_files" ]; then
    exit 0
fi

# Implementation changed but no tests — warn
echo "TDD check: implementation files changed but no test files were modified:" >&2
echo "$impl_files" | sed 's/^/  /' >&2
echo "" >&2
echo "Write a failing test first, then implement. If this is a pure refactor" >&2
echo "with no behavior change, explain why no test is needed." >&2
exit 2
