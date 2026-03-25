#!/usr/bin/env bash
# Detect new test functions missing t.Parallel().
# Scans staged test file content when STAGED_ONLY=1 (pre-commit hook).
# Otherwise scans modified test files in the working tree (CI / manual).
set -euo pipefail

# Exceptions: TestMain is setup, not a real test. Subprocess helpers
# (e.g., TestMainCLISubprocessHelper) don't run as standalone tests.
EXCEPTION_PATTERN="^func TestMain\b|SubprocessHelper"

if [ "${STAGED_ONLY:-}" = "1" ]; then
  # Get new func Test lines from the staged diff (added lines only).
  new_funcs=$(git diff --cached --diff-filter=ACM -U0 -- '*_test.go' \
    ':!third_party/' ':!vendor/' |
    grep -E '^\+func Test[A-Z]' | grep -vF '+++' |
    sed 's/^\+//' || true)
else
  # Full scan: find all Test functions in modified test files.
  modified=$(git diff --name-only HEAD -- '*_test.go' \
    ':!third_party/' ':!vendor/' 2>/dev/null || true)
  if [ -z "$modified" ]; then
    exit 0
  fi
  new_funcs=$(grep -hE '^func Test[A-Z]' $modified 2>/dev/null || true)
fi

if [ -z "$new_funcs" ]; then
  exit 0
fi

# Filter out exceptions.
new_funcs=$(echo "$new_funcs" | grep -vE "$EXCEPTION_PATTERN" || true)
if [ -z "$new_funcs" ]; then
  exit 0
fi

# For each new test function, check if the staged file has t.Parallel()
# within the first few lines of the function body.
violations=""
while IFS= read -r func_line; do
  # Extract function name: "func TestFoo(t *testing.T) {" -> "TestFoo"
  func_name=$(echo "$func_line" | sed 's/func \(Test[A-Za-z0-9_]*\).*/\1/')

  # Find which staged test files contain this function.
  files=$(git diff --cached --name-only --diff-filter=ACM -- '*_test.go' \
    ':!third_party/' ':!vendor/' || true)
  for f in $files; do
    [ -f "$f" ] || continue
    # Get the line number of the function declaration.
    line_num=$(grep -n "^func ${func_name}(" "$f" | head -1 | cut -d: -f1)
    [ -z "$line_num" ] && continue

    # Check the next 5 lines for t.Parallel().
    if ! sed -n "$((line_num+1)),$((line_num+5))p" "$f" | grep -q 't\.Parallel()'; then
      violations="${violations}\n  ${f}:${line_num}: ${func_name}"
    fi
    break
  done
done <<< "$new_funcs"

if [ -n "$violations" ]; then
  echo "ERROR: New test functions missing t.Parallel()."
  echo "  All tests must call t.Parallel() to prevent hidden shared-state"
  echo "  dependencies. If the test truly cannot be parallel, add a comment"
  echo "  explaining why (e.g., // Not parallel: mutates process-global X)."
  echo ""
  echo "  Missing t.Parallel():"
  echo -e "$violations"
  exit 1
fi
