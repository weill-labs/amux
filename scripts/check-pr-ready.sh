#!/usr/bin/env bash

set -euo pipefail

usage() {
    cat >&2 <<'EOF'
usage: scripts/check-pr-ready.sh [--no-notify] [--claude-login-regex REGEX] [--repo OWNER/REPO]
EOF
}

die() {
    echo "scripts/check-pr-ready.sh: $*" >&2
    exit 2
}

require_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        die "missing required command: $1"
    fi
}

notify=true
claude_login_regex="claude"
gh_repo=()
repo_override=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --no-notify)
            notify=false
            shift
            ;;
        --claude-login-regex)
            [[ $# -ge 2 ]] || {
                usage
                exit 2
            }
            claude_login_regex="$2"
            shift 2
            ;;
        --repo|-R)
            [[ $# -ge 2 ]] || {
                usage
                exit 2
            }
            repo_override="$2"
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
if ! pr_json="$(gh pr list "${gh_repo[@]}" --limit 200 --json number,title,url,mergeable)"; then
    die "failed to query open PRs"
fi

repo_from_url() {
    local url="$1"
    printf '%s\n' "$url" | sed -E 's#^https?://[^/]+/([^/]+/[^/]+)/pull/[0-9]+/?$#\1#'
}

repo_slug_for_pr() {
    local url="$1"

    if [[ -n "$repo_override" ]]; then
        printf '%s\n' "$repo_override"
        return
    fi
    if [[ -n "${GH_REPO:-}" ]]; then
        printf '%s\n' "$GH_REPO"
        return
    fi
    repo_from_url "$url"
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

required_checks_pass() {
    local pr_number="$1"
    local checks_json status

    set +e
    checks_json="$(gh pr checks "$pr_number" "${gh_repo[@]}" --required --json bucket,name,state 2>/dev/null)"
    status=$?
    set -e

    if [[ "$status" -ne 0 && "$status" -ne 8 ]]; then
        return 1
    fi
    if [[ -z "$checks_json" ]]; then
        return 1
    fi

    jq -e '
        length > 0 and
        all(.[]; (.bucket // "") == "pass" or (.bucket // "") == "skipping")
    ' <<<"$checks_json" >/dev/null 2>&1
}

latest_claude_review_body() {
    local repo="$1"
    local pr_number="$2"
    local reviews_json

    if ! reviews_json="$(GH_REPO="$repo" gh api --paginate --slurp "repos/{owner}/{repo}/pulls/$pr_number/reviews?per_page=100" 2>/dev/null)"; then
        return 1
    fi

    jq -r --arg pattern "$claude_login_regex" '
        (if (length > 0 and (.[0] | type) == "array") then [.[].[]] else . end)
        | [.[]
            | select((.user.login // "") | test($pattern; "i"))]
        | sort_by(.submitted_at // "")
        | last
        | .body // ""
    ' <<<"$reviews_json"
}

review_ends_with_lgtm() {
    local body="$1"

    printf '%s' "$body" |
        jq -Rsre '
            gsub("\r"; "") |
            sub("\\s+$"; "") |
            test("(^|[^[:alnum:]_])LGTM$")
        ' >/dev/null 2>&1
}

ready_message() {
    local pr_number="$1"
    printf 'PR #%s is ready for human merge. CI is green, Claude left LGTM, and there are no merge conflicts.' "$pr_number"
}

ready_count=0
while IFS= read -r pr; do
    [[ -n "$pr" ]] || continue

    number="$(jq -r '.number' <<<"$pr")"
    title="$(jq -r '.title' <<<"$pr")"
    url="$(jq -r '.url' <<<"$pr")"
    mergeable="$(jq -r '.mergeable' <<<"$pr")"

    if [[ "$mergeable" != "MERGEABLE" ]]; then
        continue
    fi
    if ! required_checks_pass "$number"; then
        continue
    fi

    repo_slug="$(repo_slug_for_pr "$url")"
    if [[ -z "$repo_slug" || "$repo_slug" == "$url" ]]; then
        continue
    fi

    review_body="$(latest_claude_review_body "$repo_slug" "$number" || true)"
    if ! review_ends_with_lgtm "$review_body"; then
        continue
    fi

    ready_count=$((ready_count + 1))
    panes="${pr_to_panes[$number]-}"
    if [[ -z "$panes" ]]; then
        printf 'PR #%s "%s" owner=orphaned state=unknown review=LGTM notify=skipped-orphaned\n' "$number" "$title"
        continue
    fi

    IFS=' ' read -r -a pane_array <<<"$panes"
    for pane in "${pane_array[@]}"; do
        IFS=$'\t' read -r state current_command <<<"$(pane_state "$pane")"
        notify_state="disabled"

        if [[ "$notify" == true ]]; then
            if [[ "$state" == "idle" ]]; then
                if amux send-keys "$pane" "$(ready_message "$number")" Enter >/dev/null 2>&1; then
                    notify_state="sent"
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

        printf 'PR #%s "%s" owner=%s state=%s review=LGTM notify=%s\n' "$number" "$title" "$pane" "$state" "$notify_state"
    done
done < <(printf '%s\n' "$pr_json" | jq -c '.[]')

if [[ "$ready_count" -eq 0 ]]; then
    echo "No open PRs are ready for human merge."
    exit 0
fi

echo "Found $ready_count open PR(s) ready for human merge."
exit 0
