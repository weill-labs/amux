# Mailbox Drain Gate

The mailbox drain gate is an optional Stop-hook integration for agents running
inside amux panes. It nudges an agent to read and ack pending amux mailbox work
before the agent parks at the end of a turn.

The gate uses the native amux mailbox exposed by `amux msg` and `amux wait msg`.
It does not require any external MCP mail server. In particular, a failure to
start an MCP server named `mcp_agent_mail` is unrelated to this hook and does
not indicate that amux mailbox delivery is broken.

The default contract is intentionally **block-once / best-effort**:

- `amux msg drain-status` reports pending mailbox work where a delivery is
  unread or unacked.
- The hook blocks the first Stop for each distinct `pending_fingerprint`.
- If the agent stops again with the same unchanged fingerprint, the hook
  releases so a stuck or non-cooperative agent cannot wedge the session.
- If the agent makes progress, such as reading a message without acking it, the
  fingerprint changes and the hook gives one fresh nudge for the remaining work.
- Set `AMUX_MAILBOX_DRAIN_STRICT=1` to block every Stop until `pending == 0`.
- Set `AMUX_MAILBOX_DRAIN_DISABLE=1` to always release.

The Stop drain gate and wake-from-park watcher are separate paths:

- The Stop gate catches already-visible pending mail at the Stop boundary.
- The Claude Code `asyncRewake` watcher parks in `amux wait msg`, then rechecks
  `amux msg drain-status --format json` after a fresh delivery. If pending
  read/ack work remains, it exits `2` so Claude wakes.
- The server also sends a delivery-time prompt nudge to quiet panes tagged
  `mailbox_wake=prompt`. Agent startup hooks set that metadata themselves; it
  does not depend on Orca metadata. The nudge fires only when the recipient's
  pending mailbox was empty before the new delivery, so a burst of messages does
  not stack repeated prompts.

## Bootstrap Pattern

When the responder has no automatic wake path, arm the responder before the
initiator sends the first message:

```bash
# Run from the responder pane, or pass the responder pane explicitly.
amux wait msg "${AMUX_PANE:-pane-2}" --timeout 120s --format json
amux msg drain-status --format json
amux msg read <id>
amux msg ack <id> --status seen
```

Then have the initiator send or reply. The important ordering is responder wait
first, initiator send second. If the send happens before the wait is armed and
there is no rewake/nudge integration, the delivery remains pending until the
responder checks `amux msg drain-status` on its own.

## Core Command

```bash
amux msg drain-status [pane]
amux msg drain-status [pane] --format json
```

Text output is a bare pending count. JSON output includes:

- `unread` (integer): deliveries that still need `msg read`.
- `unacked` (integer): deliveries that still need `msg ack`.
- `pending` (integer): deliveries that need read or ack.
- `pending_fingerprint` (string): opaque digest over pending IDs plus
  read/ack-needed state.
- `pending_ids` (array of strings): full sorted pending ID list.
- `latest` (array of summary objects): bounded summary-only records. Bodies and
  metadata values are never included.

## Mailbox JSON Schemas

`amux msg inbox [pane] --format json` and its alias
`amux msg list [pane] --format json` return an array of summary objects. Each
summary object has these fields:

- `id` (string): message ID, for example `msg-000001`.
- `sender` (object): pane address with `id` (integer), `name` (string), and
  optional `host` (string).
- `recipient` (object): pane address with `id` (integer), `name` (string), and
  optional `host` (string).
- `subject` (string), `topics` (array of strings, optional), `groups` (array of
  strings, optional).
- `thread_id` (string), `in_reply_to` (string, optional).
- `created_at` (string) and `delivered_at` (string): RFC3339Nano timestamps.
- `read_at` (string, optional) and `acked_at` (string, optional): RFC3339Nano
  timestamps.
- `ack_status` (string, optional), `ack_note` (string, optional).
- `body_size` (integer), `part_count` (integer).

`amux msg read <msg-id> --for <pane> --format json` returns one message object
with these fields:

- `id` (string), `sender` (pane address object), `recipients` (array of pane
  address objects), `subject` (string).
- `topics` (array of strings, optional), `groups` (array of strings, optional).
- `thread_id` (string), `in_reply_to` (string, optional).
- `created_at` (string) and `read_at` (string, optional): RFC3339Nano
  timestamps.
- `body` (string), `body_size` (integer), `part_count` (integer).
- `delivery` (object): `message_id` (string), `recipient` (pane address object),
  `delivered_at` (string), `read_at` (string, optional), `acked_at` (string,
  optional), `ack_status` (string, optional), `ack_note` (string, optional), and
  `last_event_seq` (integer, optional).
- `metadata` (object, optional): message metadata keyed by sender-provided
  names.

`amux msg drain-status [pane] --format json` returns one status object:

- `unread` (integer), `unacked` (integer), `pending` (integer).
- `pending_fingerprint` (string): changes when the pending read/ack set changes.
- `pending_ids` (array of strings): sorted message IDs that still need read or
  ack work.
- `latest` (array of summary objects): same summary schema as inbox/list, with
  truncated subjects and no bodies or metadata values.

## Claude Code

The repo dogfoods the Claude Code recipe through `.claude/settings.json`:

```json
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": ".claude/hooks/mailbox-wake-opt-in.sh"
          }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": ".claude/hooks/mailbox-drain.sh"
          },
          {
            "type": "command",
            "command": ".claude/hooks/mailbox-rewake.sh",
            "asyncRewake": true,
            "timeout": 86400
          }
        ]
      }
    ]
  }
}
```

`.claude/hooks/mailbox-wake-opt-in.sh` marks the current amux pane with
`mailbox_wake=prompt` during `SessionStart`. That makes delivery-time nudges use
the server-side `msg send` wake path.

On block, `.claude/hooks/mailbox-drain.sh` writes the reason to stderr and exits
`2`, which asks Claude Code to continue instead of stopping.

`.claude/hooks/mailbox-rewake.sh` is the wake-from-park companion. Claude runs
it as an `asyncRewake` Stop hook, so the command can keep waiting in the
background after the session parks. The script:

- exits 0 when `AMUX_PANE` is absent, `AMUX_MAILBOX_DRAIN_DISABLE=1`,
  `AMUX_MAILBOX_REWAKE_DISABLE=1`, required tools are missing, or amux output
  is malformed.
- uses one watcher lock per `AMUX_SESSION` + `AMUX_PANE` + session socket
  identity, so repeated Stop hook firings do not stack duplicate watchers.
- snapshots current pending message IDs, waits for a newer delivery with
  `amux wait msg`, then runs `amux msg drain-status --format json`.
- dedupes by `pending_fingerprint`, sharing the same marker as the Stop drain
  gate so unchanged pending work does not produce repeated reminders.
- prints only a bounded command reminder. It does not include mailbox bodies,
  subjects, sender metadata, or message metadata.

### Global install (all sessions)

The project recipe above only fires inside this repo, because its wrapper finds
the library via `git rev-parse`. To run the gate in **every** Claude Code session
regardless of working directory, install it under `~/.claude/` and source the
library through a stable path instead. Symlink the repo library into the hooks
directory so it tracks the repo on every `git pull`, then drop in a
path-agnostic wrapper that sources the sibling symlink:

```bash
AMUX_REPO=$(git -C /path/to/amux rev-parse --show-toplevel)
mkdir -p ~/.claude/hooks
ln -sfn "$AMUX_REPO/docs/integrations/amux-mailbox-drain-lib.sh" \
  ~/.claude/hooks/amux-mailbox-drain-lib.sh

cat > ~/.claude/hooks/amux-mailbox-drain.sh <<'EOF'
#!/usr/bin/env bash
set -u
HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HOOK_DIR/amux-mailbox-drain-lib.sh"
amux_mailbox_drain_main claude "$@"
EOF
chmod +x ~/.claude/hooks/amux-mailbox-drain.sh
```

Replace `/path/to/amux` with your local amux checkout (or run the `git -C` line
from inside the repo). An unsubstituted path creates a dangling symlink: because
the wrapper uses `set -u` without `set -e`, the failed `source` continues and the
undefined function call exits `127` — an error, not the fail-open path. Confirm
the link resolves with `readlink ~/.claude/hooks/amux-mailbox-drain-lib.sh`.

Then add a `Stop` hook with the **absolute** wrapper path to
`~/.claude/settings.json` (global hooks run from arbitrary directories, so a
relative path will not resolve):

```json
{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "/Users/you/.claude/hooks/amux-mailbox-drain.sh"
          }
        ]
      }
    ]
  }
}
```

Use your real home directory in the `command` path — `/Users/you` is macOS; on
Linux it is `/home/<username>` (run `echo $HOME`). The hooks JSON is strict JSON
and does not accept comments, so substitute the path rather than annotating it. A
wrong path is silently skipped with no startup error.

Claude Code merges global and project `Stop` hooks, so inside the amux repo both
the global and the project recipe fire. The shared per-`AMUX_SESSION` +
`AMUX_PANE` + socket marker dedupes them into a single nudge, so the redundancy
is harmless.

## Codex

The repo ships a Codex project-local example in `.codex/hooks.json`:

```json
{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "\"$(git rev-parse --show-toplevel)/.codex/hooks/amux-mailbox-wake-opt-in.sh\"",
            "timeout": 10,
            "statusMessage": "Enabling amux mailbox wake"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "\"$(git rev-parse --show-toplevel)/.codex/hooks/amux-mailbox-drain.sh\"",
            "timeout": 10,
            "statusMessage": "Checking amux mailbox"
          }
        ]
      }
    ]
  }
}
```

`.codex/hooks/amux-mailbox-wake-opt-in.sh` marks the current amux pane with
`mailbox_wake=prompt` during `SessionStart`. This is the earliest project-local
Codex hook path available in current Codex; the actual delivery nudge still
comes from the server-side `msg send` path.

On block, `.codex/hooks/amux-mailbox-drain.sh` prints valid JSON on stdout:

```json
{"decision":"block","reason":"..."}
```

Codex requires project-local hooks to be reviewed and trusted with `/hooks`
before they run. Stop hooks expect JSON on stdout when they exit successfully;
diagnostics from this recipe go to the bounded log instead.

Codex does not currently run long-lived async rewake hooks, so the project
`hooks.json` wires only the Stop drain gate. For parked Codex panes, amux uses
the server-side delivery nudge described above when the pane has opted in with
`mailbox_wake=prompt`. If project hooks are not trusted yet, or if the running
Codex version/environment does not fire `SessionStart` before the first user
prompt, a mailbox message sent before that first user prompt can remain pending
without an immediate prompt nudge. The Stop drain gate still catches pending
mail at the end of a turn. For a manually started Codex pane, opt in with:

```bash
amux meta set pane-2 mailbox_wake=prompt
```

The repo also ships `.codex/hooks/amux-mailbox-rewake.sh` as a reusable Codex
companion for environments that can run a long-lived rewake command. It shares
the Claude watcher logic, but emits Codex Stop-hook JSON:

```json
{"decision":"block","reason":"..."}
```

### Global install (all sessions)

Install under `~/.codex/` to run the gate in every Codex session. Use the same
symlinked-library + path-agnostic-wrapper pattern as the Claude Code global
recipe, but pass `codex` mode so the hook emits the `{"decision":"block"}` JSON
Codex expects:

```bash
AMUX_REPO=$(git -C /path/to/amux rev-parse --show-toplevel)
mkdir -p ~/.codex/hooks
ln -sfn "$AMUX_REPO/docs/integrations/amux-mailbox-drain-lib.sh" \
  ~/.codex/hooks/amux-mailbox-drain-lib.sh

cat > ~/.codex/hooks/amux-mailbox-drain.sh <<'EOF'
#!/usr/bin/env bash
set -u
HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HOOK_DIR/amux-mailbox-drain-lib.sh"
amux_mailbox_drain_main codex "$@"
EOF
chmod +x ~/.codex/hooks/amux-mailbox-drain.sh

cat > ~/.codex/hooks/amux-mailbox-wake-opt-in.sh <<'EOF'
#!/usr/bin/env bash
set -u
HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HOOK_DIR/amux-mailbox-drain-lib.sh"
amux_mailbox_wake_opt_in_main "$@"
EOF
chmod +x ~/.codex/hooks/amux-mailbox-wake-opt-in.sh
```

If your Codex runner supports a long-lived rewake hook, install a sibling
rewake wrapper:

```bash
cat > ~/.codex/hooks/amux-mailbox-rewake.sh <<'EOF'
#!/usr/bin/env bash
set -u
HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HOOK_DIR/amux-mailbox-drain-lib.sh"
amux_mailbox_rewake_main codex "$@"
EOF
chmod +x ~/.codex/hooks/amux-mailbox-rewake.sh
```

As above, substitute `/path/to/amux` with your checkout and confirm the link
resolves with `readlink ~/.codex/hooks/amux-mailbox-drain-lib.sh` — a dangling
symlink makes the wrapper exit `127` rather than fail open.

Then add `SessionStart` and `Stop` hooks with **absolute** wrapper paths to
`~/.codex/hooks.json`:

```json
{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/Users/you/.codex/hooks/amux-mailbox-wake-opt-in.sh",
            "timeout": 10,
            "statusMessage": "Enabling amux mailbox wake"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/Users/you/.codex/hooks/amux-mailbox-drain.sh",
            "timeout": 10,
            "statusMessage": "Checking amux mailbox"
          }
        ]
      }
    ]
  }
}
```

As with the Claude Code recipe, use your real home directory in the `command`
path — `/Users/you` is macOS; on Linux it is `/home/<username>` (run
`echo $HOME`). A wrong path is silently skipped.

Run `/hooks` in Codex once to review and trust the global hook before it fires.

## Shared Hook Behavior

The drain and rewake wrappers source
`docs/integrations/amux-mailbox-drain-lib.sh`.

The shared logic:

- exits 0 when `AMUX_PANE` is absent, the disable env var is set, or there is no
  pending work.
- fails open on missing `amux`, `jq`, `flock`, timeout support, socket errors, or
  malformed command output.
- writes bounded diagnostics to
  `${XDG_STATE_HOME:-$HOME/.local/state}/amux/mailbox-drain/hook.log`.
- stores one marker per `AMUX_SESSION` + `AMUX_PANE` + session socket identity,
  protected by `flock`, so stale markers from older same-name sessions do not
  suppress the first nudge in a fresh session.
- only uses message IDs, sender names, body sizes, and quoted/truncated subjects
  in model-visible output.

The rewake watcher output is stricter than the Stop drain output: it tells the
agent to run `amux msg drain-status --format json`, then
`amux msg read <id> --for <pane>` and
`amux msg ack <id> --for <pane> --status seen` for the pending IDs from that
JSON. It intentionally omits the IDs and summaries from the hook output.

Fail-open means a global install carries no risk: a session launched from a
stripped-`PATH` context (cron, launchd) where `amux`, `jq`, `flock`, or `timeout`
are not resolvable simply releases the stop — the gate is skipped, never wedged.
If you want the gate to fire in such contexts, ensure the launch environment puts
those tools on `PATH` (or prepend their directories in the wrapper before the
`. "$HOOK_DIR/..."` line).

Run a local sanity check (project or global wrapper, whichever you installed):

```bash
.claude/hooks/mailbox-drain.sh --self-test            # project recipe
.claude/hooks/mailbox-rewake.sh --self-test           # project rewake watcher
.claude/hooks/mailbox-wake-opt-in.sh                  # project wake opt-in
.codex/hooks/amux-mailbox-wake-opt-in.sh              # project wake opt-in
.codex/hooks/amux-mailbox-drain.sh --self-test        # project recipe
.codex/hooks/amux-mailbox-rewake.sh --self-test       # project rewake wrapper
~/.claude/hooks/amux-mailbox-drain.sh --self-test     # global recipe
~/.codex/hooks/amux-mailbox-drain.sh --self-test      # global recipe
```

The self-test validates local tools and that the installed `amux` binary knows
the required mailbox commands; it does not require pending mail. Run it from a
login shell so the wrapper sees your full `PATH`.
