# Mailbox Drain Gate

The mailbox drain gate is an optional Stop-hook integration for agents running
inside amux panes. It nudges an agent to read and ack pending amux mailbox work
before the agent parks at the end of a turn.

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

## Core Command

```bash
amux msg drain-status [pane]
amux msg drain-status [pane] --format json
```

Text output is a bare pending count. JSON output includes:

- `unread`: deliveries that still need `msg read`.
- `unacked`: deliveries that still need `msg ack`.
- `pending`: deliveries that need read or ack.
- `pending_fingerprint`: opaque digest over pending IDs plus read/ack-needed
  state.
- `pending_ids`: full sorted pending ID list.
- `latest`: bounded summary-only records. Bodies and metadata values are never
  included.

## Claude Code

The repo dogfoods the Claude Code recipe through `.claude/settings.json`:

```json
{
  "hooks": {
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

Claude Code merges global and project `Stop` hooks, so inside the amux repo both
the global and the project recipe fire. The shared per-`AMUX_SESSION` +
`AMUX_PANE` + socket marker dedupes them into a single nudge, so the redundancy
is harmless.

## Codex

The repo ships a Codex project-local example in `.codex/hooks.json`:

```json
{
  "hooks": {
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

On block, `.codex/hooks/amux-mailbox-drain.sh` prints valid JSON on stdout:

```json
{"decision":"block","reason":"..."}
```

Codex requires project-local hooks to be reviewed and trusted with `/hooks`
before they run. Stop hooks expect JSON on stdout when they exit successfully;
diagnostics from this recipe go to the bounded log instead.

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
```

Then add a `Stop` hook with the **absolute** wrapper path to `~/.codex/hooks.json`:

```json
{
  "hooks": {
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

Run `/hooks` in Codex once to review and trust the global hook before it fires.

## Shared Hook Behavior

Both wrappers source `docs/integrations/amux-mailbox-drain-lib.sh`.

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

<<<<<<< HEAD
The Claude rewake watcher also sources the shared library. Its model-visible
output is stricter than the Stop drain output: it tells Claude to run
`amux msg drain-status --format json`, then `amux msg read <id> --for <pane>` and
`amux msg ack <id> --for <pane> --status seen` for the pending IDs from that
JSON. It intentionally omits the IDs and summaries from the hook output.

Run a local sanity check:

```bash
.claude/hooks/mailbox-drain.sh --self-test
.claude/hooks/mailbox-rewake.sh --self-test
.codex/hooks/amux-mailbox-drain.sh --self-test
```

The self-test validates local tools and that the installed `amux` binary knows
the required mailbox commands; it does not require pending mail.
=======
Fail-open means a global install carries no risk: a session launched from a
stripped-`PATH` context (cron, launchd) where `amux`, `jq`, `flock`, or `timeout`
are not resolvable simply releases the stop — the gate is skipped, never wedged.
If you want the gate to fire in such contexts, ensure the launch environment puts
those tools on `PATH` (or prepend their directories in the wrapper before the
`. "$HOOK_DIR/..."` line).

Run a local sanity check (project or global wrapper, whichever you installed):

```bash
.claude/hooks/mailbox-drain.sh --self-test          # project recipe
.codex/hooks/amux-mailbox-drain.sh --self-test       # project recipe
~/.claude/hooks/amux-mailbox-drain.sh --self-test     # global recipe
~/.codex/hooks/amux-mailbox-drain.sh --self-test      # global recipe
```

The self-test validates local tools and that the installed `amux` binary knows
`msg drain-status`; it does not require pending mail. Run it from a login shell
so the wrapper sees your full `PATH`.
>>>>>>> 29c52ec (Document global install of mailbox drain hooks)
