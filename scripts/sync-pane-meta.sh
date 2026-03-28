#!/usr/bin/env bash

set -euo pipefail

pane="${1:-${AMUX_PANE:-}}"
if [[ -z "$pane" ]]; then
    exit 0
fi

if ! command -v amux >/dev/null 2>&1; then
    exit 0
fi

if ! command -v jq >/dev/null 2>&1; then
    exit 0
fi

capture="$(amux capture --format json "$pane" 2>/dev/null || true)"
if [[ -z "$capture" ]]; then
    exit 0
fi

cwd="$(printf '%s\n' "$capture" | jq -r '.cwd // empty' 2>/dev/null)"
timestamp="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

tracked_prs_json='[]'
pr_numbers=()
while IFS= read -r number; do
    if [[ -n "$number" ]]; then
        pr_numbers+=("$number")
    fi
done < <(printf '%s\n' "$capture" | jq -r '(.meta.tracked_prs // [])[].number' 2>/dev/null)
if (( ${#pr_numbers[@]} > 0 )); then
    pr_entries=()
    for number in "${pr_numbers[@]}"; do
        status="unknown"
        stale=true
        if command -v gh >/dev/null 2>&1; then
            if [[ -n "$cwd" && -d "$cwd" ]]; then
                if merged_at="$(cd "$cwd" && gh pr view "$number" --json mergedAt --jq .mergedAt 2>/dev/null)"; then
                    if [[ -n "$merged_at" && "$merged_at" != "null" ]]; then
                        status="completed"
                        stale=false
                    else
                        status="active"
                        stale=false
                    fi
                fi
            else
                if merged_at="$(gh pr view "$number" --json mergedAt --jq .mergedAt 2>/dev/null)"; then
                    if [[ -n "$merged_at" && "$merged_at" != "null" ]]; then
                        status="completed"
                        stale=false
                    else
                        status="active"
                        stale=false
                    fi
                fi
            fi
        fi
        pr_entries+=("$(jq -cn \
            --argjson number "$number" \
            --arg status "$status" \
            --arg checked_at "$timestamp" \
            --argjson stale "$stale" \
            '$stale
            | {number: $number, status: $status, checked_at: $checked_at}
            | if .status == "unknown" then . + {stale: true} else . end')")
    done
    tracked_prs_json="$(printf '%s\n' "${pr_entries[@]}" | jq -cs '.')"
fi

tracked_issues_json='[]'
issue_ids=()
while IFS= read -r issue; do
    if [[ -n "$issue" ]]; then
        issue_ids+=("$issue")
    fi
done < <(printf '%s\n' "$capture" | jq -r '(.meta.tracked_issues // [])[].id' 2>/dev/null)
if (( ${#issue_ids[@]} > 0 )); then
    issue_entries=()
    for issue in "${issue_ids[@]}"; do
        status="unknown"
        stale=true
        if [[ -n "${LINEAR_API_KEY:-}" ]] && command -v curl >/dev/null 2>&1; then
            payload="$(jq -cn --arg id "$issue" '{query: "query($id: String!) { issue(id: $id) { state { type } } }", variables: {id: $id}}')"
            if response="$(curl -fsS \
                -H "Authorization: ${LINEAR_API_KEY}" \
                -H "Content-Type: application/json" \
                -d "$payload" \
                https://api.linear.app/graphql 2>/dev/null)"; then
                state_type="$(printf '%s\n' "$response" | jq -r '.data.issue.state.type // empty' 2>/dev/null)"
                if [[ -n "$state_type" ]]; then
                    if [[ "${state_type,,}" == "completed" ]]; then
                        status="completed"
                    else
                        status="active"
                    fi
                    stale=false
                fi
            fi
        fi
        issue_entries+=("$(jq -cn \
            --arg id "$issue" \
            --arg status "$status" \
            --arg checked_at "$timestamp" \
            --argjson stale "$stale" \
            '$stale
            | {id: $id, status: $status, checked_at: $checked_at}
            | if .status == "unknown" then . + {stale: true} else . end')")
    done
    tracked_issues_json="$(printf '%s\n' "${issue_entries[@]}" | jq -cs '.')"
fi

args=("set-kv" "$pane")
if (( ${#pr_numbers[@]} > 0 )); then
    args+=("tracked_prs=$tracked_prs_json")
fi
if (( ${#issue_ids[@]} > 0 )); then
    args+=("tracked_issues=$tracked_issues_json")
fi
if (( ${#args[@]} == 2 )); then
    exit 0
fi

amux "${args[@]}" >/dev/null
