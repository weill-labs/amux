#!/usr/bin/env bash

set -euo pipefail

usage() {
    cat >&2 <<'EOF'
usage: scripts/check-claude-review.sh [--watch] [--repo OWNER/REPO] [PR]

Reports the latest Claude review verdict on a PR by parsing GitHub PR comments.

Exit codes:
  0  latest Claude review is LGTM
  1  latest Claude review still has findings
  2  usage or dependency error
  3  no PR or no Claude review found
  4  latest Claude review could not be classified
  124 watch mode timed out waiting for a new Claude review
EOF
}

die() {
    echo "scripts/check-claude-review.sh: $*" >&2
    exit 2
}

require_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        die "missing required command: $1"
    fi
}

watch=false
pr_ref=""
gh_repo=()
poll_interval="${AMUX_CLAUDE_REVIEW_POLL_INTERVAL:-10}"
watch_timeout="${AMUX_CLAUDE_REVIEW_TIMEOUT:-0}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --watch)
            watch=true
            shift
            ;;
        --repo|-R)
            [[ $# -ge 2 ]] || {
                usage
                exit 2
            }
            gh_repo=(-R "$2")
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        -*)
            usage
            exit 2
            ;;
        *)
            if [[ -n "$pr_ref" ]]; then
                usage
                exit 2
            fi
            pr_ref="$1"
            shift
            ;;
    esac
done

require_cmd gh
require_cmd jq

fetch_pr_json() {
    local gh_args=(pr view)
    if [[ -n "$pr_ref" ]]; then
        gh_args+=("$pr_ref")
    fi
    gh_args+=("${gh_repo[@]}" --json number,url,comments)

    gh "${gh_args[@]}" 2>/dev/null || true
}

extract_latest_claude_review() {
    jq -cr '
        def is_claude_review:
            (.author.login // "") as $author |
            (.body // "") as $body |
            (
                ($author == "claude") or
                ($author == "claude[bot]") or
                ($author == "github-actions[bot]") or
                ($author == "github-actions")
            ) and ($body | startswith("**Claude finished "));

        def verdict:
            (.body // "") as $body |
            if ($body | test("(^|\\n)LGTM\\s*$"; "m")) or ($body | contains("No blocking issues.")) then
                "lgtm"
            elif ($body | test("(^|\\n)### Findings(\\n|$)"; "m")) or ($body | test("\\*\\*Blocking:"; "m")) then
                "findings"
            else
                "unknown"
            end;

        (.comments // [])
        | map(select(is_claude_review))
        | sort_by(.createdAt)
        | last
        | if . == null then
            empty
          else
            {
                id,
                author: (.author.login // ""),
                createdAt: (.createdAt // ""),
                url: (.url // ""),
                verdict: verdict
            }
          end
    '
}

print_review_summary() {
    local pr_json="$1"
    local review_json="$2"
    local pr_number pr_url comment_id author verdict created_at review_url

    pr_number="$(jq -r '.number' <<<"$pr_json")"
    pr_url="$(jq -r '.url // ""' <<<"$pr_json")"
    comment_id="$(jq -r '.id' <<<"$review_json")"
    author="$(jq -r '.author' <<<"$review_json")"
    verdict="$(jq -r '.verdict' <<<"$review_json")"
    created_at="$(jq -r '.createdAt' <<<"$review_json")"
    review_url="$(jq -r '.url' <<<"$review_json")"

    printf 'pr=%s verdict=%s author=%s comment_id=%s created_at=%s url=%s pr_url=%s\n' \
        "$pr_number" \
        "$verdict" \
        "$author" \
        "$comment_id" \
        "$created_at" \
        "$review_url" \
        "$pr_url"
}

exit_for_verdict() {
    case "$1" in
        lgtm)
            exit 0
            ;;
        findings)
            exit 1
            ;;
        *)
            exit 4
            ;;
    esac
}

pr_json="$(fetch_pr_json)"
if [[ -z "$pr_json" || "$pr_json" == "null" ]]; then
    echo "No PR found for the current branch. Create one with \`gh pr create\`, then rerun \`scripts/check-claude-review.sh\`."
    exit 3
fi

latest_review_json="$(extract_latest_claude_review <<<"$pr_json")"
if [[ "$watch" != true ]]; then
    if [[ -z "$latest_review_json" ]]; then
        pr_number="$(jq -r '.number' <<<"$pr_json")"
        echo "No Claude review comment found on PR #$pr_number."
        exit 3
    fi
    print_review_summary "$pr_json" "$latest_review_json"
    exit_for_verdict "$(jq -r '.verdict' <<<"$latest_review_json")"
fi

pr_number="$(jq -r '.number' <<<"$pr_json")"
baseline_id=""
if [[ -n "$latest_review_json" ]]; then
    baseline_id="$(jq -r '.id' <<<"$latest_review_json")"
    echo "Watching PR #$pr_number for a new Claude review after comment $baseline_id"
else
    echo "Watching PR #$pr_number for the first Claude review comment"
fi

deadline=0
if [[ "$watch_timeout" != "0" ]]; then
    deadline=$((SECONDS + watch_timeout))
fi

while :; do
    sleep "$poll_interval"

    pr_json="$(fetch_pr_json)"
    if [[ -z "$pr_json" || "$pr_json" == "null" ]]; then
        if (( deadline != 0 && SECONDS >= deadline )); then
            echo "Timed out waiting for a new Claude review on PR #$pr_number."
            exit 124
        fi
        continue
    fi

    latest_review_json="$(extract_latest_claude_review <<<"$pr_json")"
    if [[ -n "$latest_review_json" ]]; then
        latest_id="$(jq -r '.id' <<<"$latest_review_json")"
        if [[ "$latest_id" != "$baseline_id" ]]; then
            print_review_summary "$pr_json" "$latest_review_json"
            exit_for_verdict "$(jq -r '.verdict' <<<"$latest_review_json")"
        fi
    fi

    if (( deadline != 0 && SECONDS >= deadline )); then
        echo "Timed out waiting for a new Claude review on PR #$pr_number."
        exit 124
    fi
done
