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

amux_mailbox_drain_read_status_json() {
    local json pending

    json="$(amux_mailbox_drain_run msg drain-status --format json)" || return 1
    pending="$(printf '%s' "$json" | jq -r '.pending // empty')" || return 1
    if [ -z "$pending" ]; then
        return 1
    fi
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
