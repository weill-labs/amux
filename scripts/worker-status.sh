#!/usr/bin/env bash

set -euo pipefail

codex_trust_dialog_question="Do you trust the contents of this directory?"
codex_trust_dialog_warning="higher risk of prompt injection."
table_format='%-20s %-16s %-10s %-8s %s\n'
vt_idle_probe_timeout="50ms"

die() {
    echo "scripts/worker-status.sh: $*" >&2
    exit 2
}

require_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        die "missing required command: $1"
    fi
}

truncate_cell() {
    local value="$1"
    local max="$2"

    if (( ${#value} <= max )); then
        printf '%s' "$value"
        return
    fi

    if (( max <= 3 )); then
        printf '%s' "${value:0:max}"
        return
    fi

    printf '%s...' "${value:0:max-3}"
}

is_worker_name() {
    local name="$1"
    printf '%s\n' "$name" | grep -Eqi '(^|[-_])worker($|[-_])'
}

is_worker_pane() {
    local name="$1"
    local task="$2"

    [[ "$task" == "worker" ]] || is_worker_name "$name"
}

pane_vt_idle() {
    local pane="$1"
    amux wait vt-idle "$pane" --settle 0s --timeout "$vt_idle_probe_timeout" >/dev/null 2>&1
}

require_cmd amux
require_cmd jq

if ! pane_list="$(amux list --no-cwd)"; then
    die "failed to list panes"
fi

printf "$table_format" "PANE" "ISSUE" "STATE" "PR" "LAST OUTPUT"

while IFS= read -r pane; do
    [[ -n "$pane" ]] || continue

    if ! capture="$(amux capture --format json "$pane" 2>/dev/null)"; then
        continue
    fi
    if ! jq -e '.error == null' >/dev/null 2>&1 <<<"$capture"; then
        continue
    fi

    IFS=$'\t' read -r pane_name task issue pr idle child_count dialog_visible last_output <<<"$(jq -r \
        --arg question "$codex_trust_dialog_question" \
        --arg warning "$codex_trust_dialog_warning" '
        [
            (.name // ""),
            (.task // .meta.task // ""),
            (
                if (.meta.tracked_issues // []) | length > 0 then
                    (.meta.tracked_issues | map(.id) | join(","))
                else
                    "-"
                end
            ),
            (
                if (.meta.tracked_prs // []) | length > 0 then
                    (.meta.tracked_prs | map(.number | tostring) | join(","))
                elif (.meta.pr // .pr // "") != "" then
                    (.meta.pr // .pr)
                else
                    "-"
                end
            ),
            (if .idle == true then "true" else "false" end),
            (((.child_pids // []) | length) | tostring),
            (
                if (
                    ([.content[]? | select(contains($question))] | length) > 0 and
                    ([.content[]? | select(contains($warning))] | length) > 0
                ) then
                    "true"
                else
                    "false"
                end
            ),
            (
                [.content[]? |
                    gsub("[[:space:]]+"; " ") |
                    sub("^ "; "") |
                    sub(" $"; "") |
                    select(length > 0)
                ] | last // "-"
            )
        ] | @tsv
    ' <<<"$capture")"

    if [[ -z "$pane_name" ]]; then
        pane_name="$pane"
    fi
    if ! is_worker_pane "$pane_name" "$task"; then
        continue
    fi

    state="busy"
    if [[ "$idle" == "true" ]]; then
        state="idle"
    elif pane_vt_idle "$pane"; then
        state="vt-idle"
    fi

    if [[ "$state" == "vt-idle" && "$child_count" -gt 0 && "$dialog_visible" == "true" ]]; then
        state="stuck"
    fi

    pane_cell="$(truncate_cell "$pane_name" 20)"
    issue_cell="$(truncate_cell "$issue" 16)"
    state_cell="$(truncate_cell "$state" 10)"
    pr_cell="$(truncate_cell "$pr" 8)"
    output_cell="$(truncate_cell "$last_output" 96)"

    printf "$table_format" \
        "$pane_cell" \
        "$issue_cell" \
        "$state_cell" \
        "$pr_cell" \
        "$output_cell"
done < <(printf '%s\n' "$pane_list" | awk 'NR > 1 && $2 != "" { print $2 }')
