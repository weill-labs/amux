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

This is not a wake-from-park mechanism. It catches already-visible pending mail
at the Stop boundary. Mail delivered after the final check waits until the next
turn or another watcher/user prompt wakes the agent.

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
          }
        ]
      }
    ]
  }
}
```

On block, `.claude/hooks/mailbox-drain.sh` writes the reason to stderr and exits
`2`, which asks Claude Code to continue instead of stopping.

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

Run a local sanity check:

```bash
.claude/hooks/mailbox-drain.sh --self-test
.codex/hooks/amux-mailbox-drain.sh --self-test
```

The self-test validates local tools and that the installed `amux` binary knows
`msg drain-status`; it does not require pending mail.
