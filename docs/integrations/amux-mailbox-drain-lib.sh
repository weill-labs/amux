#!/usr/bin/env bash

amux_mailbox_drain_state_dir() {
    printf '%s/amux/mailbox-drain\n' "${XDG_STATE_HOME:-$HOME/.local/state}"
}

amux_mailbox_drain_log() {
    local msg="$1"
    local dir log tmp

    dir="$(amux_mailbox_drain_state_dir)"
    mkdir -p "$dir" 2>/dev/null || return 0
    log="$dir/hook.log"
    printf '%s %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$msg" >>"$log" 2>/dev/null || return 0
    if [ "$(wc -l <"$log" 2>/dev/null || echo 0)" -gt 200 ]; then
        tmp="$log.tmp.$$"
        tail -n 200 "$log" >"$tmp" 2>/dev/null && mv "$tmp" "$log" 2>/dev/null || rm -f "$tmp"
    fi
}

amux_mailbox_drain_timeout_bin() {
    command -v timeout 2>/dev/null || command -v gtimeout 2>/dev/null
}

amux_mailbox_drain_run() {
    local timeout_bin

    timeout_bin="$(amux_mailbox_drain_timeout_bin)" || return 127
    "$timeout_bin" "${AMUX_MAILBOX_DRAIN_TIMEOUT:-8s}" amux "$@"
}

amux_mailbox_drain_socket_path() {
    local dir session uid

    session="${AMUX_SESSION:-main}"
    dir="${AMUX_SOCKET_DIR:-}"
    if [ -z "$dir" ]; then
        uid="$(id -u 2>/dev/null || printf unknown)"
        dir="/tmp/amux-$uid"
    fi
    printf '%s/%s\n' "$dir" "$session"
}

amux_mailbox_drain_socket_identity() {
    local path identity

    path="$(amux_mailbox_drain_socket_path)"
    identity="$(stat -c '%d:%i:%Y' "$path" 2>/dev/null)" ||
        identity="$(stat -f '%d:%i:%m' "$path" 2>/dev/null)" ||
        identity=""
    printf '%s\n' "$identity"
}

amux_mailbox_drain_key() {
    printf '%s-%s-%s' "${AMUX_SESSION:-main}" "${AMUX_PANE:-unknown}" "$(amux_mailbox_drain_socket_identity)" | tr -c 'A-Za-z0-9_.-' '_'
}

amux_mailbox_drain_marker_paths() {
    local dir key

    dir="$(amux_mailbox_drain_state_dir)"
    key="$(amux_mailbox_drain_key)"
    printf '%s\n%s\n' "$dir/$key.marker" "$dir/$key.lock"
}

amux_mailbox_rewake_lock_path() {
    local dir key

    dir="$(amux_mailbox_drain_state_dir)"
    key="$(amux_mailbox_drain_key)"
    printf '%s/%s.rewake.lock\n' "$dir" "$key"
}

amux_mailbox_drain_self_test() {
    local missing=0

    for cmd in amux jq flock; do
        if ! command -v "$cmd" >/dev/null 2>&1; then
            printf 'missing required command: %s\n' "$cmd" >&2
            missing=1
        fi
    done
    if ! amux_mailbox_drain_timeout_bin >/dev/null 2>&1; then
        printf 'missing required command: timeout or gtimeout\n' >&2
        missing=1
    fi
    if [ "$missing" -ne 0 ]; then
        return 1
    fi
    if ! amux msg drain-status --help >/dev/null 2>&1; then
        printf 'amux does not support msg drain-status\n' >&2
        return 1
    fi
    printf 'amux mailbox drain hook self-test passed\n'
}

amux_mailbox_rewake_self_test() {
    if ! amux_mailbox_drain_self_test >/dev/null; then
        amux_mailbox_drain_self_test
        return $?
    fi
    if ! amux wait --help 2>/dev/null | grep -q 'msg'; then
        printf 'amux does not support wait msg\n' >&2
        return 1
    fi
    printf 'amux mailbox rewake hook self-test passed\n'
}

amux_mailbox_drain_read_status_json() {
    local json pending

    json="$(amux_mailbox_drain_run msg drain-status --format json)" || return 1
    pending="$(printf '%s' "$json" | jq -r '.pending // empty' 2>/dev/null)" || return 1
    case "$pending" in
    '' | *[!0-9]*)
        return 1
        ;;
    esac
    printf '%s\n' "$json"
}

amux_mailbox_drain_reason() {
    local json="$1"

    printf '%s' "$json" | jq -r --arg pane "$AMUX_PANE" '
      . as $s
      | ($s.latest // []) as $latest
      | ($s.pending_ids // []) as $ids
      | ($ids[0:10]) as $shown_ids
      | def sender_name:
          (.sender.name // (if .sender.id then "pane-" + (.sender.id | tostring) else "unknown" end));
        "amux mailbox has \($s.pending) pending message(s) for pane \($pane). Read and ack mailbox work before stopping.\n\n"
        + "Pending summaries:\n"
        + (if ($latest | length) == 0 then "- (summary unavailable)"
           else ([$latest[] | "- " + (.id | tostring) + " from " + (sender_name | @json) + ": " + (.body_size | tostring) + " bytes, subject " + ((.subject // "") | @json)] | join("\n"))
           end)
        + (if $s.pending > ($latest | length) then "\n- ... plus " + (($s.pending - ($latest | length)) | tostring) + " more pending message(s)" else "" end)
        + "\n\nFor each pending message, run read then ack. Commands for the first " + (($shown_ids | length) | tostring) + " pending id(s):\n"
        + (if ($shown_ids | length) == 0 then "amux msg drain-status --format json"
           else ([$shown_ids[] | "amux msg read " + . + " --for " + $pane + "\namux msg ack " + . + " --for " + $pane + " --status seen"] | join("\n"))
           end)
        + "\n\nCheck remaining work with: amux msg drain-status --format json"
    '
}

amux_mailbox_drain_emit_block() {
    local mode="$1"
    local reason="$2"

    case "$mode" in
    codex)
        jq -cn --arg reason "$reason" '{decision:"block",reason:$reason}'
        ;;
    claude)
        printf '%s\n' "$reason" >&2
        return 2
        ;;
    *)
        printf '%s\n' "$reason" >&2
        return 2
        ;;
    esac
}

amux_mailbox_drain_main() {
    local mode="${1:-claude}"
    local pending json fingerprint reason marker lock dir previous paths err

    shift || true
    if [ "${1:-}" = "--self-test" ]; then
        amux_mailbox_drain_self_test
        return $?
    fi

    if [ "${AMUX_MAILBOX_DRAIN_DISABLE:-}" = "1" ] || [ -z "${AMUX_PANE:-}" ]; then
        return 0
    fi
    if ! command -v amux >/dev/null 2>&1; then
        amux_mailbox_drain_log "amux not found; releasing stop"
        return 0
    fi
    if ! command -v jq >/dev/null 2>&1; then
        amux_mailbox_drain_log "jq not found; releasing stop"
        return 0
    fi
    if ! command -v flock >/dev/null 2>&1; then
        amux_mailbox_drain_log "flock not found; releasing stop"
        return 0
    fi
    if ! amux_mailbox_drain_timeout_bin >/dev/null 2>&1; then
        amux_mailbox_drain_log "timeout not found; releasing stop"
        return 0
    fi

    err="$(mktemp "${TMPDIR:-/tmp}/amux-mailbox-drain.XXXXXX")" || return 0
    if ! pending="$(amux_mailbox_drain_run msg drain-status 2>"$err")"; then
        amux_mailbox_drain_log "drain-status count failed: $(tr '\n' ' ' <"$err")"
        rm -f "$err"
        return 0
    fi
    rm -f "$err"
    pending="$(printf '%s' "$pending" | tr -d '[:space:]')"
    case "$pending" in
    '' | *[!0-9]*)
        amux_mailbox_drain_log "invalid drain-status count: $pending"
        return 0
        ;;
    esac

    paths="$(amux_mailbox_drain_marker_paths)"
    marker="$(printf '%s\n' "$paths" | sed -n '1p')"
    lock="$(printf '%s\n' "$paths" | sed -n '2p')"
    dir="$(dirname "$marker")"

    if [ "$pending" -eq 0 ]; then
        rm -f "$marker" 2>/dev/null || true
        return 0
    fi

    if ! json="$(amux_mailbox_drain_read_status_json)"; then
        amux_mailbox_drain_log "drain-status json failed; releasing stop"
        return 0
    fi
    pending="$(printf '%s' "$json" | jq -r '.pending // 0')" || return 0
    if [ "$pending" -eq 0 ]; then
        rm -f "$marker" 2>/dev/null || true
        return 0
    fi
    fingerprint="$(printf '%s' "$json" | jq -r '.pending_fingerprint // empty')" || return 0
    if [ -z "$fingerprint" ]; then
        amux_mailbox_drain_log "missing pending_fingerprint; releasing stop"
        return 0
    fi

    mkdir -p "$dir" 2>/dev/null || {
        amux_mailbox_drain_log "cannot create state directory; releasing stop"
        return 0
    }
    exec 9>"$lock" || {
        amux_mailbox_drain_log "cannot open marker lock; releasing stop"
        return 0
    }
    if ! flock -w "${AMUX_MAILBOX_DRAIN_LOCK_TIMEOUT:-2}" 9; then
        amux_mailbox_drain_log "cannot acquire marker lock; releasing stop"
        exec 9>&-
        return 0
    fi

    if [ "${AMUX_MAILBOX_DRAIN_STRICT:-}" != "1" ]; then
        previous="$(cat "$marker" 2>/dev/null || true)"
        if [ "$previous" = "$fingerprint" ]; then
            exec 9>&-
            return 0
        fi
    fi
    printf '%s\n' "$fingerprint" >"$marker" 2>/dev/null || {
        amux_mailbox_drain_log "cannot write marker; releasing stop"
        exec 9>&-
        return 0
    }
    exec 9>&-

    reason="$(amux_mailbox_drain_reason "$json")" || {
        amux_mailbox_drain_log "cannot build drain reason; releasing stop"
        return 0
    }
    amux_mailbox_drain_emit_block "$mode" "$reason"
}

amux_mailbox_rewake_deps_available() {
    if ! command -v amux >/dev/null 2>&1; then
        amux_mailbox_drain_log "amux not found; releasing rewake"
        return 1
    fi
    if ! command -v jq >/dev/null 2>&1; then
        amux_mailbox_drain_log "jq not found; releasing rewake"
        return 1
    fi
    if ! command -v flock >/dev/null 2>&1; then
        amux_mailbox_drain_log "flock not found; releasing rewake"
        return 1
    fi
    if ! amux_mailbox_drain_timeout_bin >/dev/null 2>&1; then
        amux_mailbox_drain_log "timeout not found; releasing rewake"
        return 1
    fi
    return 0
}

amux_mailbox_rewake_after_cursor() {
    local json="$1"

    printf '%s' "$json" | jq -r '
      if ((.pending_ids // []) | type) != "array" then
        error("pending_ids must be an array")
      elif ((.pending_ids // []) | length) > 0 then
        (.pending_ids // [])[-1]
      else
        ""
      end
    ' 2>/dev/null
}

amux_mailbox_rewake_wait_delivery() {
    local after="$1"
    local timeout="${AMUX_MAILBOX_REWAKE_WAIT_TIMEOUT:-24h}"
    local err status

    err="$(mktemp "${TMPDIR:-/tmp}/amux-mailbox-rewake-wait.XXXXXX")" || return 1
    if [ -n "$after" ]; then
        amux wait msg "$AMUX_PANE" --after "$after" --timeout "$timeout" --format json > /dev/null 2>"$err"
    else
        amux wait msg "$AMUX_PANE" --timeout "$timeout" --format json > /dev/null 2>"$err"
    fi
    status=$?
    if [ "$status" -ne 0 ]; then
        amux_mailbox_drain_log "wait msg failed: $(tr '\n' ' ' <"$err")"
        rm -f "$err"
        return 1
    fi
    rm -f "$err"
    return 0
}

amux_mailbox_rewake_parse_pending() {
    local json="$1"
    local pending

    pending="$(printf '%s' "$json" | jq -r '.pending // empty' 2>/dev/null)" || return 1
    case "$pending" in
    '' | *[!0-9]*)
        return 1
        ;;
    esac
    printf '%s\n' "$pending"
}

amux_mailbox_rewake_mark_fingerprint_once() {
    local fingerprint="$1"
    local marker lock dir paths previous

    paths="$(amux_mailbox_drain_marker_paths)"
    marker="$(printf '%s\n' "$paths" | sed -n '1p')"
    lock="$(printf '%s\n' "$paths" | sed -n '2p')"
    dir="$(dirname "$marker")"

    mkdir -p "$dir" 2>/dev/null || {
        amux_mailbox_drain_log "cannot create rewake state directory; releasing rewake"
        return 1
    }
    exec 9>"$lock" || {
        amux_mailbox_drain_log "cannot open rewake marker lock; releasing rewake"
        return 1
    }
    if ! flock -w "${AMUX_MAILBOX_DRAIN_LOCK_TIMEOUT:-2}" 9; then
        amux_mailbox_drain_log "cannot acquire rewake marker lock; releasing rewake"
        exec 9>&-
        return 1
    fi
    previous="$(cat "$marker" 2>/dev/null || true)"
    if [ "$previous" = "$fingerprint" ]; then
        exec 9>&-
        return 2
    fi
    printf '%s\n' "$fingerprint" >"$marker" 2>/dev/null || {
        amux_mailbox_drain_log "cannot write rewake marker; releasing rewake"
        exec 9>&-
        return 1
    }
    exec 9>&-
    return 0
}

amux_mailbox_rewake_clear_marker() {
    local paths marker

    paths="$(amux_mailbox_drain_marker_paths)"
    marker="$(printf '%s\n' "$paths" | sed -n '1p')"
    rm -f "$marker" 2>/dev/null || true
}

amux_mailbox_rewake_reason() {
    local pending="$1"
    local pane="$AMUX_PANE"

    cat <<EOF
amux mailbox delivery arrived for pane $pane; $pending pending read/ack item(s) remain.

Run:
amux msg drain-status --format json

Then read and ack each pending ID from that JSON:
amux msg read <id> --for $pane
amux msg ack <id> --for $pane --status seen
EOF
}

amux_mailbox_rewake_emit_wake() {
    local reason="$1"

    printf '%s\n' "$reason" >&2
    return 2
}

amux_mailbox_rewake_main() {
    local initial after lock dir json pending fingerprint reason mark_status

    if [ "${1:-}" = "--self-test" ]; then
        amux_mailbox_rewake_self_test
        return $?
    fi

    if [ "${AMUX_MAILBOX_DRAIN_DISABLE:-}" = "1" ] ||
        [ "${AMUX_MAILBOX_REWAKE_DISABLE:-}" = "1" ] ||
        [ -z "${AMUX_PANE:-}" ]; then
        return 0
    fi
    amux_mailbox_rewake_deps_available || return 0

    lock="$(amux_mailbox_rewake_lock_path)"
    dir="$(dirname "$lock")"
    mkdir -p "$dir" 2>/dev/null || {
        amux_mailbox_drain_log "cannot create rewake lock directory; releasing rewake"
        return 0
    }
    exec 8>"$lock" || {
        amux_mailbox_drain_log "cannot open rewake lock; releasing rewake"
        return 0
    }
    if ! flock -n 8; then
        amux_mailbox_drain_log "rewake watcher already active"
        exec 8>&-
        return 0
    fi

    initial="$(amux_mailbox_drain_read_status_json)" || {
        amux_mailbox_drain_log "initial rewake drain-status failed; releasing rewake"
        exec 8>&-
        return 0
    }
    after="$(amux_mailbox_rewake_after_cursor "$initial")" || {
        amux_mailbox_drain_log "initial rewake drain-status malformed; releasing rewake"
        exec 8>&-
        return 0
    }

    amux_mailbox_rewake_wait_delivery "$after" || {
        exec 8>&-
        return 0
    }

    json="$(amux_mailbox_drain_read_status_json)" || {
        amux_mailbox_drain_log "rewake drain-status failed after delivery; releasing rewake"
        exec 8>&-
        return 0
    }
    pending="$(amux_mailbox_rewake_parse_pending "$json")" || {
        amux_mailbox_drain_log "rewake drain-status malformed after delivery; releasing rewake"
        exec 8>&-
        return 0
    }
    if [ "$pending" -eq 0 ]; then
        amux_mailbox_rewake_clear_marker
        exec 8>&-
        return 0
    fi

    fingerprint="$(printf '%s' "$json" | jq -r '.pending_fingerprint // empty' 2>/dev/null)" || {
        amux_mailbox_drain_log "cannot parse rewake fingerprint; releasing rewake"
        exec 8>&-
        return 0
    }
    if [ -z "$fingerprint" ]; then
        amux_mailbox_drain_log "missing rewake pending_fingerprint; releasing rewake"
        exec 8>&-
        return 0
    fi

    amux_mailbox_rewake_mark_fingerprint_once "$fingerprint"
    mark_status=$?
    if [ "$mark_status" -eq 2 ]; then
        exec 8>&-
        return 0
    fi
    if [ "$mark_status" -ne 0 ]; then
        exec 8>&-
        return 0
    fi

    reason="$(amux_mailbox_rewake_reason "$pending")" || {
        amux_mailbox_drain_log "cannot build rewake reason; releasing rewake"
        exec 8>&-
        return 0
    }
    exec 8>&-
    amux_mailbox_rewake_emit_wake "$reason"
}
