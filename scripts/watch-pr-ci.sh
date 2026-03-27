#!/usr/bin/env bash

set -euo pipefail

if ! command -v gh >/dev/null 2>&1; then
    echo "gh is required to watch PR CI." >&2
    exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
    echo "jq is required to inspect PR CI output." >&2
    exit 1
fi

interval="${AMUX_PR_CHECK_INTERVAL:-10}"
failed_run_limit="${AMUX_PR_FAILED_RUNS_LIMIT:-3}"
pr_ref="${1:-}"

pr_json="$(
    if [[ -n "$pr_ref" ]]; then
        gh pr view "$pr_ref" --json number,url,headRefName,headRefOid 2>/dev/null || true
    else
        gh pr view --json number,url,headRefName,headRefOid 2>/dev/null || true
    fi
)"

if [[ -z "$pr_json" || "$pr_json" == "null" ]]; then
    echo "No PR found for the current branch. Create one with \`gh pr create\`, then rerun \`scripts/watch-pr-ci.sh\`."
    exit 0
fi

pr_num="$(printf '%s\n' "$pr_json" | jq -r '.number')"
pr_url="$(printf '%s\n' "$pr_json" | jq -r '.url')"
head_sha="$(printf '%s\n' "$pr_json" | jq -r '.headRefOid')"

if gh pr checks "$pr_num" --required --watch --interval "$interval"; then
    echo "PR #$pr_num CI passed: $pr_url"
    exit 0
fi

echo "PR #$pr_num CI failed: $pr_url"
echo
echo "Failed required checks:"

checks_json="$(gh pr checks "$pr_num" --required --json name,link,bucket,state,workflow 2>/dev/null || true)"
if [[ -n "$checks_json" ]] && printf '%s\n' "$checks_json" | jq -e 'length > 0' >/dev/null 2>&1; then
    if ! printf '%s\n' "$checks_json" | jq -e '.[] | select(.bucket == "fail")' >/dev/null 2>&1; then
        echo "- GitHub reported a failure, but no failed required checks were returned."
    else
        while IFS=$'\t' read -r workflow name link; do
            line="- $name"
            if [[ -n "$workflow" && "$workflow" != "null" ]]; then
                line="$line ($workflow)"
            fi
            if [[ -n "$link" && "$link" != "null" ]]; then
                line="$line: $link"
            fi
            echo "$line"
        done < <(
            printf '%s\n' "$checks_json" |
                jq -r '.[] | select(.bucket == "fail") | [.workflow, .name, .link] | @tsv'
        )
    fi
else
    echo "- Unable to fetch required-check details from gh."
fi

run_json="$(gh run list --commit "$head_sha" --status failure --json databaseId,workflowName,displayTitle,url,conclusion 2>/dev/null || true)"
if [[ -n "$run_json" ]] && printf '%s\n' "$run_json" | jq -e 'length > 0' >/dev/null 2>&1; then
    echo
    echo "Failed run logs:"
    while IFS=$'\t' read -r run_id display_title workflow_name run_url; do
        title="$display_title"
        if [[ -z "$title" || "$title" == "null" ]]; then
            title="$workflow_name"
        fi
        echo
        echo "== $title =="
        if [[ -n "$run_url" && "$run_url" != "null" ]]; then
            echo "$run_url"
        fi
        if ! gh run view "$run_id" --log-failed; then
            echo "Unable to fetch failed log for run $run_id." >&2
        fi
    done < <(
        printf '%s\n' "$run_json" |
            jq -r --argjson limit "$failed_run_limit" '.[0:$limit] | .[] | [.databaseId, .displayTitle, .workflowName, .url] | @tsv'
    )
else
    echo
    echo "No failed runs were returned for head SHA $head_sha."
fi

echo
echo "Fix the failure, rerun the relevant tests locally, then push again with \`scripts/push-and-watch-ci.sh\`."
exit 1
