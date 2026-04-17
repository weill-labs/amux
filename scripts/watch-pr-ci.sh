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
heartbeat_interval="${AMUX_PR_HEARTBEAT_INTERVAL:-30}"
run_discovery_timeout="${AMUX_PR_RUN_DISCOVERY_TIMEOUT:-60}"
run_discovery_interval="${AMUX_PR_RUN_DISCOVERY_INTERVAL:-5}"
failed_run_limit="${AMUX_PR_FAILED_RUNS_LIMIT:-3}"
pr_ref="${1:-}"
printed_logs=0
head_runs_json=""
latest_checks_json=""
poll_output=""
poll_status=0

declare -A last_check_state=()
last_waiting_line=""
pr_repo=""
base_ref=""

current_epoch() {
    date +%s
}

sleep_seconds() {
    local seconds="$1"
    if (( seconds > 0 )); then
        sleep "$seconds"
    fi
}

json_is_array() {
    local json="$1"
    [[ -n "$json" ]] && printf '%s\n' "$json" | jq -e 'type == "array"' >/dev/null 2>&1
}

json_has_items() {
    local json="$1"
    [[ -n "$json" ]] && printf '%s\n' "$json" | jq -e 'length > 0' >/dev/null 2>&1
}

is_graphql_rate_limit_error() {
    local output="$1"
    [[ "$output" == *"GraphQL:"*"rate limit"* ]] ||
        [[ "$output" == *"secondary rate limit"* ]] ||
        [[ "$output" == *"API rate limit exceeded"* ]]
}

repo_slug_from_pr_url() {
    local url="$1"
    if [[ "$url" =~ github\.com/([^/]+/[^/]+)/pull/ ]]; then
        printf '%s\n' "${BASH_REMATCH[1]}"
        return 0
    fi
    return 1
}

urlencode_component() {
    local value="$1"
    jq -nr --arg value "$value" '$value | @uri'
}

check_rows_tsv() {
    local checks_json="$1"
    printf '%s\n' "$checks_json" |
        jq -r '.[] | [(.name // .workflow // "check"), (.bucket // ""), (.state // ""), (.startedAt // ""), (.completedAt // "")] | @tsv'
}

wait_for_head_runs() {
    local want_sha="$1"
    local deadline=$(( $(current_epoch) + run_discovery_timeout ))
    local run_json
    while (( $(current_epoch) < deadline )); do
        run_json="$(gh run list --commit "$want_sha" --json databaseId,workflowName,displayTitle,url,conclusion,status -L "$failed_run_limit" 2>/dev/null || true)"
        if json_has_items "$run_json"; then
            printf '%s\n' "$run_json"
            return 0
        fi
        sleep_seconds "$run_discovery_interval"
    done
    return 1
}

poll_required_checks() {
    local pr_number="$1"
    set +e
    poll_output="$(gh pr checks "$pr_number" --required --json name,link,bucket,state,workflow,startedAt,completedAt 2>&1)"
    poll_status=$?
    set -e
}

rest_required_checks_json() {
    local repo="$1"
    local sha="$2"
    local base_branch="$3"
    local branch_ref required_json check_runs_json status_json required_contexts

    [[ -n "$repo" && -n "$sha" ]] || return 1

    branch_ref="$(urlencode_component "$base_branch")"
    required_json="$(gh api "repos/$repo/branches/$branch_ref/protection/required_status_checks" 2>/dev/null || true)"
    check_runs_json="$(gh api "repos/$repo/commits/$sha/check-runs?per_page=100" 2>/dev/null || true)"
    status_json="$(gh api "repos/$repo/commits/$sha/status" 2>/dev/null || true)"

    [[ -n "$check_runs_json" ]] || check_runs_json='{}'
    [[ -n "$status_json" ]] || status_json='{}'

    required_contexts="$(
        if [[ -n "$required_json" ]]; then
            printf '%s\n' "$required_json" |
                jq -c '((.checks // []) | map(.context)) + (.contexts // []) | map(select(. != null and . != "")) | unique' 2>/dev/null || printf '[]\n'
        else
            printf '[]\n'
        fi
    )"

    jq -cn \
        --argjson check_runs "$check_runs_json" \
        --argjson statuses "$status_json" \
        --argjson required "$required_contexts" '
        def bucket_from_check:
            if (.status // "") != "completed" then "pending"
            elif (.conclusion // "") == "success" then "pass"
            elif (.conclusion // "") == "cancelled" then "cancel"
            elif ((.conclusion // "") == "neutral" or (.conclusion // "") == "skipped") then "skipping"
            else "fail"
            end;
        def bucket_from_status:
            if (.state // "") == "success" then "pass"
            elif (.state // "") == "pending" then "pending"
            else "fail"
            end;
        [
            (($statuses.statuses // []) | map({
                name: (.context // "status"),
                link: (.target_url // ""),
                bucket: bucket_from_status,
                state: (.state // ""),
                workflow: "status",
                startedAt: (.created_at // .updated_at // ""),
                completedAt: (if (.state // "") == "pending" then "" else (.updated_at // "") end)
            })[]),
            (($check_runs.check_runs // []) | map({
                name: (.name // "check"),
                link: (.html_url // ""),
                bucket: bucket_from_check,
                state: (.status // .conclusion // ""),
                workflow: (.app.slug // .app.name // "check"),
                startedAt: (.started_at // ""),
                completedAt: (.completed_at // "")
            })[])
        ]
        | flatten
        | reduce .[] as $check ({}; .[$check.name] = $check)
        | [.[]]
        | if ($required | length) > 0 then
            map(select(.name as $name | $required | index($name)))
          else
            .
          end
        | sort_by(.name)
    '
}

timestamp_to_epoch() {
    local timestamp="$1"
    [[ -n "$timestamp" && "$timestamp" != "null" ]] || return 1
    jq -nr --arg timestamp "$timestamp" '$timestamp | fromdateiso8601' 2>/dev/null
}

format_duration() {
    local total="$1"
    local hours minutes seconds

    if (( total < 0 )); then
        total=0
    fi

    hours=$((total / 3600))
    minutes=$(((total % 3600) / 60))
    seconds=$((total % 60))

    if (( hours > 0 )); then
        printf '%dh%dm%ds' "$hours" "$minutes" "$seconds"
        return
    fi
    if (( minutes > 0 )); then
        printf '%dm%ds' "$minutes" "$seconds"
        return
    fi
    printf '%ds' "$seconds"
}

classify_check_phase() {
    local bucket="${1,,}"
    local state="${2,,}"
    local started_at="$3"

    case "$bucket" in
        pass|fail|cancel|skipping)
            printf 'completed'
            return
            ;;
    esac

    case "$state" in
        queued|pending|requested|waiting|expected)
            printf 'queued'
            return
            ;;
        in_progress|running|started)
            printf 'in_progress'
            return
            ;;
    esac

    if [[ -n "$started_at" && "$started_at" != "null" ]]; then
        printf 'in_progress'
        return
    fi

    printf 'queued'
}

format_completion_bucket() {
    case "${1,,}" in
        pass)
            printf 'pass'
            ;;
        fail)
            printf 'fail'
            ;;
        cancel)
            printf 'cancelled'
            ;;
        skipping)
            printf 'skipped'
            ;;
        *)
            printf '%s' "${1,,}"
            ;;
    esac
}

format_transition_summary() {
    local bucket="$1"
    local state="$2"
    local started_at="$3"
    local phase

    phase="$(classify_check_phase "$bucket" "$state" "$started_at")"
    case "$phase" in
        queued)
            printf 'queued'
            ;;
        in_progress)
            printf 'in_progress'
            ;;
        completed)
            printf 'completed (%s)' "$(format_completion_bucket "$bucket")"
            ;;
    esac
}

format_status_summary() {
    local bucket="$1"
    local state="$2"
    local started_at="$3"
    local now="$4"
    local phase start_epoch elapsed

    phase="$(classify_check_phase "$bucket" "$state" "$started_at")"
    case "$phase" in
        queued)
            printf 'queued'
            ;;
        in_progress)
            start_epoch="$(timestamp_to_epoch "$started_at" || true)"
            if [[ -n "$start_epoch" ]]; then
                elapsed=$((now - start_epoch))
                printf 'in_progress (%s)' "$(format_duration "$elapsed")"
                return
            fi
            printf 'in_progress'
            ;;
        completed)
            printf 'completed (%s)' "$(format_completion_bucket "$bucket")"
            ;;
    esac
}

print_check_updates() {
    local checks_json="$1"
    local pending_line
    local name bucket state started_at completed_at summary

    pending_line="$(
        printf '%s\n' "$checks_json" |
            jq -r '[.[] | select((.bucket // "") == "pending") | (.name // .workflow // "check")] | join(", ")'
    )"
    if [[ -n "$pending_line" && "$pending_line" != "$last_waiting_line" ]]; then
        echo "Waiting for: $pending_line"
    fi
    last_waiting_line="$pending_line"

    while IFS=$'\t' read -r name bucket state started_at completed_at; do
        summary="$(format_transition_summary "$bucket" "$state" "$started_at")"
        if [[ "${last_check_state[$name]-}" == "$summary" ]]; then
            continue
        fi
        echo "$name: $summary"
        last_check_state["$name"]="$summary"
    done < <(check_rows_tsv "$checks_json")
}

render_heartbeat_line() {
    local checks_json="$1"
    local now="$2"
    local name bucket state started_at completed_at
    local -a parts=()
    local joined=""

    while IFS=$'\t' read -r name bucket state started_at completed_at; do
        parts+=("$name: $(format_status_summary "$bucket" "$state" "$started_at" "$now")")
    done < <(check_rows_tsv "$checks_json")

    if ((${#parts[@]} == 0)); then
        return
    fi

    joined="${parts[0]}"
    for name in "${parts[@]:1}"; do
        joined="$joined, $name"
    done
    printf '%s' "$joined"
}

checks_have_failures() {
    local checks_json="$1"
    printf '%s\n' "$checks_json" | jq -e '.[] | select((.bucket // "") == "fail" or (.bucket // "") == "cancel")' >/dev/null 2>&1
}

checks_have_pending() {
    local checks_json="$1"
    printf '%s\n' "$checks_json" | jq -e '.[] | select((.bucket // "") == "pending")' >/dev/null 2>&1
}

watch_required_checks() {
    local pr_number="$1"
    local deadline=$(( $(current_epoch) + run_discovery_timeout ))
    local discovered=0
    local next_heartbeat=0
    local no_checks_retries=0
    local now
    local rest_output

    while :; do
        poll_required_checks "$pr_number"
        if [[ "$poll_status" -ne 0 ]] && is_graphql_rate_limit_error "$poll_output"; then
            rest_output="$(rest_required_checks_json "$pr_repo" "$head_sha" "$base_ref" || true)"
            if json_is_array "$rest_output"; then
                poll_output="$rest_output"
                poll_status=0
            fi
        fi

        if json_is_array "$poll_output" && json_has_items "$poll_output"; then
            latest_checks_json="$poll_output"
            now="$(current_epoch)"
            if (( discovered == 0 )); then
                discovered=1
                next_heartbeat=$((now + heartbeat_interval))
            fi

            print_check_updates "$poll_output"

            if checks_have_failures "$poll_output"; then
                return 1
            fi
            if ! checks_have_pending "$poll_output"; then
                return 0
            fi

            if (( heartbeat_interval > 0 && now >= next_heartbeat )); then
                echo "Heartbeat: $(render_heartbeat_line "$poll_output" "$now")"
                while (( next_heartbeat <= now )); do
                    next_heartbeat=$((next_heartbeat + heartbeat_interval))
                done
            fi

            sleep_seconds "$interval"
            continue
        fi

        if [[ "$poll_output" == *"no checks reported"* ]] || (json_is_array "$poll_output" && ! json_has_items "$poll_output"); then
            if (( no_checks_retries == 0 || $(current_epoch) < deadline )); then
                no_checks_retries=$((no_checks_retries + 1))
                sleep_seconds "$run_discovery_interval"
                continue
            fi
        fi

        if [[ -n "$poll_output" ]]; then
            printf '%s\n' "$poll_output"
        fi
        return "$poll_status"
    done
}

pr_json="$(
    if [[ -n "$pr_ref" ]]; then
        gh pr view "$pr_ref" --json number,url,headRefName,headRefOid,baseRefName 2>/dev/null || true
    else
        gh pr view --json number,url,headRefName,headRefOid,baseRefName 2>/dev/null || true
    fi
)"

if [[ -z "$pr_json" || "$pr_json" == "null" ]]; then
    echo "No PR found for the current branch. Create one with \`gh pr create\`, then rerun \`scripts/watch-pr-ci.sh\`."
    exit 0
fi

pr_num="$(printf '%s\n' "$pr_json" | jq -r '.number')"
pr_url="$(printf '%s\n' "$pr_json" | jq -r '.url')"
base_ref="$(printf '%s\n' "$pr_json" | jq -r '.baseRefName // "main"')"
pr_repo="$(repo_slug_from_pr_url "$pr_url" || true)"
head_sha="$(git rev-parse HEAD 2>/dev/null || true)"
if [[ -z "$head_sha" ]]; then
    head_sha="$(printf '%s\n' "$pr_json" | jq -r '.headRefOid')"
fi

if head_runs_json="$(wait_for_head_runs "$head_sha")"; then
    :
fi

if watch_required_checks "$pr_num"; then
    echo "PR #$pr_num CI passed: $pr_url"
    exit 0
fi

echo "PR #$pr_num CI failed: $pr_url"
echo
echo "Failed required checks:"

checks_json="$latest_checks_json"
if [[ -z "$checks_json" ]]; then
    checks_json="$(gh pr checks "$pr_num" --required --json name,link,bucket,state,workflow 2>/dev/null || true)"
fi
if [[ -z "$checks_json" || "$checks_json" == "null" ]] && is_graphql_rate_limit_error "$poll_output"; then
    checks_json="$(rest_required_checks_json "$pr_repo" "$head_sha" "$base_ref" || true)"
fi
if json_has_items "$checks_json"; then
    if ! printf '%s\n' "$checks_json" | jq -e '.[] | select(.bucket == "fail" or .bucket == "cancel")' >/dev/null 2>&1; then
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
                jq -r '.[] | select(.bucket == "fail" or .bucket == "cancel") | [.workflow, .name, .link] | @tsv'
        )
    fi
else
    echo "- Unable to fetch required-check details from gh."
fi

if [[ -n "$checks_json" ]] && printf '%s\n' "$checks_json" | jq -e '.[] | select(.bucket == "fail" and (.link // "") != "")' >/dev/null 2>&1; then
    echo
    echo "Failed run logs:"
    while IFS=$'\t' read -r name link; do
        echo
        echo "== $name =="
        echo "$link"
        if [[ "$link" =~ /actions/runs/[0-9]+/job/([0-9]+) ]]; then
            if gh run view --job "${BASH_REMATCH[1]}" --log-failed; then
                printed_logs=1
                continue
            fi
        fi
        if [[ "$link" =~ /actions/runs/([0-9]+) ]]; then
            if gh run view "${BASH_REMATCH[1]}" --log-failed; then
                printed_logs=1
                continue
            fi
        fi
        echo "Unable to fetch failed log directly from $link." >&2
    done < <(
        printf '%s\n' "$checks_json" |
            jq -r '.[] | select(.bucket == "fail" and (.link // "") != "") | [.name, .link] | @tsv'
    )
fi

if [[ "$printed_logs" -eq 0 ]]; then
    run_json="$head_runs_json"
    if [[ -z "$run_json" ]]; then
        run_json="$(gh run list --commit "$head_sha" --json databaseId,workflowName,displayTitle,url,conclusion,status -L "$failed_run_limit" 2>/dev/null || true)"
    fi
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
            if gh run view "$run_id" --log-failed; then
                printed_logs=1
                continue
            fi
            echo "Unable to fetch failed log for run $run_id." >&2
        done < <(
            printf '%s\n' "$run_json" |
                jq -r --argjson limit "$failed_run_limit" '[.[] | select((.conclusion // "") == "failure")] | .[0:$limit] | .[] | [.databaseId, .displayTitle, .workflowName, .url] | @tsv'
        )
    fi
fi

if [[ "$printed_logs" -eq 0 ]]; then
    echo
    echo "No failed runs were returned for head SHA $head_sha."
fi

echo
echo "Fix the failure, rerun the relevant tests locally, then push again with \`scripts/push-and-watch-ci.sh\`."
exit 1
