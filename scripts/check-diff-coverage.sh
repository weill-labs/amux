#!/usr/bin/env bash
set -euo pipefail

BASE_REF="origin/main"
PROFILE="merged-coverage.txt"
RUN_COVERAGE=true
FETCH_BASE=true
TARGET=""

usage() {
  cat <<'EOF'
usage: scripts/check-diff-coverage.sh [--base ref] [--profile path] [--target percent] [--reuse-existing] [--no-fetch]
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --base)
      BASE_REF="$2"
      shift 2
      ;;
    --profile)
      PROFILE="$2"
      shift 2
      ;;
    --target)
      TARGET="$2"
      shift 2
      ;;
    --reuse-existing)
      RUN_COVERAGE=false
      shift
      ;;
    --no-fetch)
      FETCH_BASE=false
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      usage >&2
      exit 1
      ;;
  esac
done

if [[ "$FETCH_BASE" == true ]]; then
  git fetch origin main >/dev/null
fi

if ! git rev-parse --verify "$BASE_REF" >/dev/null 2>&1; then
  echo "missing base ref: $BASE_REF" >&2
  exit 1
fi

if ! git diff --name-only "${BASE_REF}...HEAD" | grep -qE '\.go$'; then
  echo "Diff coverage check: no changed Go files against ${BASE_REF}"
  exit 0
fi

if [[ -z "$TARGET" ]]; then
  TARGET=$(python3 - <<'PY'
import pathlib, re, sys
text = pathlib.Path("codecov.yml").read_text()
match = re.search(r"patch:\s*\n(?:[^\n]*\n)*?\s*target:\s*([0-9.]+)%", text)
if not match:
    sys.exit("could not find codecov patch target in codecov.yml")
print(match.group(1))
PY
)
fi

coverage_outputs=(
  root-coverage.txt
  unit-coverage.txt
  integration-coverage.txt
  merged-coverage.txt
  coverage-summary.txt
)
declare -A had_file=()
for file in "${coverage_outputs[@]}"; do
  if [[ -e "$file" ]]; then
    had_file["$file"]=1
  else
    had_file["$file"]=0
  fi
done

cleanup_generated_outputs() {
  if [[ "$RUN_COVERAGE" != true ]]; then
    return
  fi
  for file in "${coverage_outputs[@]}"; do
    if [[ "${had_file[$file]}" == "0" ]]; then
      rm -f "$file"
    fi
  done
}
trap cleanup_generated_outputs EXIT

if [[ "$RUN_COVERAGE" == true ]]; then
  echo "Running merged coverage before diff coverage check..."
  bash scripts/coverage.sh --keep-files
fi

MODULE_PATH=$(go list -m)
go run ./cmd/diffcoverage \
  --base "$BASE_REF" \
  --module "$MODULE_PATH" \
  --profile "$PROFILE" \
  --target "$TARGET"
