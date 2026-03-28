#!/usr/bin/env bash

set -euo pipefail

usage() {
    cat >&2 <<'EOF'
usage: scripts/check-worker-ci.sh [--no-notify] [--wait] [--ack-substring TEXT] [--ack-timeout DURATION] [--repo OWNER/REPO]
EOF
}

die() {
    echo "scripts/check-worker-ci.sh: $*" >&2
    exit 2
}

require_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        die "missing required command: $1"
    fi
}

notify=true
wait_for_ack=false
ack_substring="Working"
ack_timeout="15s"
gh_repo=()

while [[ $# -gt 0 ]]; do
    case "$1" in
        --no-notify)
            notify=false
            shift
            ;;
        --wait)
            wait_for_ack=true
            shift
            ;;
        --ack-substring)
            [[ $# -ge 2 ]] || {
                usage
                exit 2
            }
            ack_substring="$2"
            shift 2
            ;;
        --ack-timeout)
            [[ $# -ge 2 ]] || {
                usage
                exit 2
            }
            ack_timeout="$2"
            shift 2
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
        *)
            usage
            exit 2
            ;;
    esac
done

require_cmd amux
require_cmd gh
require_cmd jq

if ! pane_list="$(amux list --no-cwd)"; then
    die "failed to list panes"
fi

declare -A pr_to_panes=()
while IFS=$'\t' read -r pr pane; do
    [[ -n "$pr" && -n "$pane" ]] || continue
    existing="${pr_to_panes[$pr]-}"
    if [[ " $existing " != *" $pane "* ]]; then
        pr_to_panes["$pr"]="${existing:+$existing }$pane"
    fi
# This parser intentionally depends on the current `amux list --no-cwd` column layout.
done < <(printf '%s\n' "$pane_list" | awk '
NR > 1 {
    pane = $2
    if (pane == "") {
        next
    }
    start = index($0, "prs=[")
    if (start == 0) {
        next
    }
    rest = substr($0, start + 5)
    stop = index(rest, "]")
    if (stop == 0) {
        next
    }
    values = substr(rest, 1, stop - 1)
    count = split(values, prs, ",")
    for (i = 1; i <= count; i++) {
        if (prs[i] != "") {
            printf "%s\t%s\n", prs[i], pane
        }
    }
}
')

# Keep the GitHub query bounded; bump this if the repo ever exceeds 200 open PRs.
if ! pr_json="$(gh pr list "${gh_repo[@]}" --limit 200 --json number,title,mergeable,statusCheckRollup)"; then
    die "failed to query open PRs"
fi

problem_rows="$(printf '%s\n' "$pr_json" | jq -c '
def failing_check:
    if .__typename == "CheckRun" then
        ((.conclusion // "") | test("^(ACTION_REQUIRED|CANCELLED|FAILURE|STALE|STARTUP_FAILURE|TIMED_OUT)$"))
    elif .__typename == "StatusContext" then
        ((.state // "") | test("^(ERROR|FAILURE)$"))
    else
        false
    end;

.[] |
{
    number,
    title,
    mergeable,
    failing_checks: [(.statusCheckRollup // [])[] | select(failing_check) | (.name // .context // "check")]
} |
.has_conflict = (.mergeable == "CONFLICTING") |
.has_failed_checks = ((.failing_checks | length) > 0) |
select(.has_conflict or .has_failed_checks)
')"

if [[ -z "$problem_rows" ]]; then
    echo "No open PRs with failing CI or merge conflicts."
    exit 0
fi

build_reason() {
    local has_conflict="$1"
    local checks="$2"
    local reason=""

    if [[ "$has_conflict" == "true" ]]; then
        reason="merge conflict"
    fi
    if [[ -n "$checks" ]]; then
        if [[ -n "$reason" ]]; then
            reason="$reason; "
        fi
        reason="${reason}failing checks: $checks"
    fi
    printf '%s' "$reason"
}

build_message() {
    local number="$1"
    local has_conflict="$2"
    local checks="$3"

    if [[ "$has_conflict" == "true" && -n "$checks" ]]; then
        printf 'PR #%s has merge conflicts and failing CI (%s). Rebase, fix, and push.' "$number" "$checks"
        return
    fi
    if [[ "$has_conflict" == "true" ]]; then
        printf 'PR #%s has merge conflicts with main. Rebase, rerun tests, and push.' "$number"
        return
    fi
    printf 'PR #%s has failing CI (%s). Fix and push.' "$number" "$checks"
}

pane_state() {
    local pane="$1"
    local capture

    if ! capture="$(amux capture --format json "$pane" 2>/dev/null)"; then
        printf 'unknown\t'
        return
    fi

    jq -r '
        if .idle == true then
            "idle\t"
        else
            "busy\t" + (.current_command // "")
        end
    ' <<<"$capture" 2>/dev/null || printf 'unknown\t'
}

problem_count=0
while IFS= read -r problem; do
    [[ -n "$problem" ]] || continue
    problem_count=$((problem_count + 1))

    number="$(jq -r '.number' <<<"$problem")"
    title="$(jq -r '.title' <<<"$problem")"
    has_conflict="$(jq -r '.has_conflict' <<<"$problem")"
    checks="$(jq -r '(.failing_checks // []) | join(", ")' <<<"$problem")"
    reason="$(build_reason "$has_conflict" "$checks")"
    message="$(build_message "$number" "$has_conflict" "$checks")"
    panes="${pr_to_panes[$number]-}"

    if [[ -z "$panes" ]]; then
        printf 'PR #%s "%s" owner=orphaned state=unknown reason="%s" notify=skipped-orphaned\n' "$number" "$title" "$reason"
        continue
    fi

    IFS=' ' read -r -a pane_array <<<"$panes"
    for pane in "${pane_array[@]}"; do
        IFS=$'\t' read -r state current_command <<<"$(pane_state "$pane")"
        notify_state="disabled"
        ack_state="n/a"

        if [[ "$notify" == true ]]; then
            if [[ "$state" == "idle" ]]; then
                if amux send-keys "$pane" "$message" Enter >/dev/null 2>&1; then
                    notify_state="sent"
                    if [[ "$wait_for_ack" == true ]]; then
                        if amux wait content "$pane" "$ack_substring" --timeout "$ack_timeout" >/dev/null 2>&1; then
                            ack_state="confirmed"
                        else
                            ack_state="timeout"
                        fi
                    fi
                else
                    notify_state="send-error"
                fi
            elif [[ "$state" == "busy" ]]; then
                notify_state="skipped-busy"
            else
                notify_state="skipped-unknown"
            fi
        fi

        if [[ "$state" == "busy" && -n "$current_command" ]]; then
            state="busy($current_command)"
        fi

        printf 'PR #%s "%s" owner=%s state=%s reason="%s" notify=%s' "$number" "$title" "$pane" "$state" "$reason" "$notify_state"
        if [[ "$wait_for_ack" == true && "$notify_state" == "sent" ]]; then
            printf ' ack=%s' "$ack_state"
        fi
        printf '\n'
    done
done <<<"$problem_rows"

echo "Found $problem_count open PR(s) with failing CI or merge conflicts."
exit 1
