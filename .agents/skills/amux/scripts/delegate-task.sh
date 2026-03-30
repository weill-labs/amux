#!/usr/bin/env bash

set -euo pipefail

usage() {
    cat >&2 <<'EOF'
usage: scripts/delegate-task.sh <pane> [--issue ISSUE] [--timeout DURATION] <task>
EOF
}

die() {
    echo "scripts/delegate-task.sh: $*" >&2
    exit 1
}

usage_error() {
    usage
    exit 2
}

require_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        die "missing required command: $1"
    fi
}

validate_duration() {
    local duration="$1"

    python3 -c '
import re
import sys


def parse_duration(raw: str) -> None:
    units = {
        "ns": 1e-9,
        "us": 1e-6,
        "µs": 1e-6,
        "μs": 1e-6,
        "ms": 1e-3,
        "s": 1.0,
        "m": 60.0,
        "h": 3600.0,
    }
    pattern = re.compile(r"([0-9]+(?:\.[0-9]+)?)(ns|us|µs|μs|ms|s|m|h)")
    pos = 0
    for match in pattern.finditer(raw):
        if match.start() != pos:
            raise ValueError(raw)
        _ = float(match.group(1)) * units[match.group(2)]
        pos = match.end()
    if pos != len(raw):
        raise ValueError(raw)


try:
    parse_duration(sys.argv[1])
except ValueError:
    print(f"invalid duration: {sys.argv[1]}", file=sys.stderr)
    sys.exit(1)
' "$duration" >/dev/null
}

wait_for_event() {
    local want_event="$1"
    local timeout="$2"

    python3 -c '
import json
import os
import re
import select
import sys
import time


def parse_duration(raw: str) -> float:
    units = {
        "ns": 1e-9,
        "us": 1e-6,
        "µs": 1e-6,
        "μs": 1e-6,
        "ms": 1e-3,
        "s": 1.0,
        "m": 60.0,
        "h": 3600.0,
    }
    pattern = re.compile(r"([0-9]+(?:\.[0-9]+)?)(ns|us|µs|μs|ms|s|m|h)")
    total = 0.0
    pos = 0
    for match in pattern.finditer(raw):
        if match.start() != pos:
            raise ValueError(raw)
        total += float(match.group(1)) * units[match.group(2)]
        pos = match.end()
    if pos != len(raw):
        raise ValueError(raw)
    return total


want = sys.argv[1]
try:
    timeout_seconds = parse_duration(sys.argv[2])
except ValueError:
    print(f"invalid duration: {sys.argv[2]}", file=sys.stderr)
    sys.exit(3)

fd = sys.stdin.fileno()
deadline = time.monotonic() + timeout_seconds
buffer = b""

while True:
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        sys.exit(1)

    readable, _, _ = select.select([fd], [], [], remaining)
    if not readable:
        sys.exit(1)

    chunk = os.read(fd, 4096)
    if not chunk:
        sys.exit(2)
    buffer += chunk

    while b"\n" in buffer:
        line, buffer = buffer.split(b"\n", 1)
        if not line.strip():
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if event.get("type") == want:
            sys.exit(0)
' "$want_event" "$timeout" <&"$events_fd"
}

pane="${1:-}"
if [[ -z "$pane" ]]; then
    usage_error
fi
shift

issue=""
timeout="3s"
subscribe_timeout="5s"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --issue)
            [[ $# -ge 2 ]] || usage_error
            issue="$2"
            shift 2
            ;;
        --timeout)
            [[ $# -ge 2 ]] || usage_error
            timeout="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        --)
            shift
            break
            ;;
        -*)
            usage_error
            ;;
        *)
            break
            ;;
    esac
done

[[ $# -ge 1 ]] || usage_error

task="$*"

require_cmd amux
require_cmd python3
if ! validate_duration "$timeout"; then
    exit 2
fi

events_pid=""
events_fd=""
cleanup() {
    if [[ -n "$events_fd" ]]; then
        exec {events_fd}<&- || true
    fi
    if [[ -n "$events_pid" ]]; then
        kill "$events_pid" >/dev/null 2>&1 || true
        wait "$events_pid" >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

coproc EVENT_STREAM { amux events --pane "$pane" --filter layout,output --throttle 0s --no-reconnect; }
events_pid="$EVENT_STREAM_PID"
exec {events_fd}<&"${EVENT_STREAM[0]}"

if ! wait_for_event "layout" "$subscribe_timeout"; then
    die "failed to subscribe to $pane event stream"
fi

amux send-keys "$pane" "$task" Enter >/dev/null

if wait_for_event "output" "$timeout"; then
    :
else
    status=$?
    if [[ $status -eq 3 ]]; then
        exit 2
    fi
    capture="$(amux capture "$pane" 2>&1 || true)"
    {
        echo "scripts/delegate-task.sh: $pane appears stuck: expected output within $timeout after sending the task."
        echo "Captured $pane:"
        printf '%s\n' "$capture"
    } >&2
    exit 1
fi

if [[ -n "$issue" ]]; then
    amux meta set "$pane" "issue=$issue" >/dev/null
fi

echo "$pane accepted task"
