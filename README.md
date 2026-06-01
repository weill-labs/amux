# amux — terminal multiplexer for human+agent workflows

[![CI](https://github.com/weill-labs/amux/actions/workflows/ci.yml/badge.svg)](https://github.com/weill-labs/amux/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/weill-labs/amux/graph/badge.svg?token=RY0CPn9v7g)](https://codecov.io/gh/weill-labs/amux)

*Picture a terminal split into panes — each running an agent, possibly on a different machine. Can one agent reliably command another, wait for the result, and read it back, while the human watches it all happen?*

GUIs force screenshots and vision models. Headless APIs cut the human out. **amux** is a shared TUI grid where humans use keybindings and agents use CLI commands. Same panes, same state.

Structured JSON capture, blocking waits, and push-based events — no polling, no screen-scraping.

![amux demo](demo/hero.gif)

## How it works

The VT emulator's parsed state is the source of truth, rendered two ways:

```
PTY output (raw bytes)
       ↓
   VT emulator (parsed state) ← source of truth
       ↓                ↓
  ANSI rendering    structured output
  (for humans)      (for agents)
```

Retained pane history is server-owned. Clients hydrate that history on attach and keep their own local copy-mode state (scroll position, search, selection) on top of it. That means history survives detach/reattach, hot reload, and crash recovery, while each viewer can still browse independently. Crash recovery restores a fresh shell for each local pane; retained history always survives, and the last visible screen is only replayed when the checkpointed pane was already idle at a shell prompt.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/weill-labs/amux/main/scripts/install-release.sh | bash
```

```bash
brew install weill-labs/tap/amux
```

```bash
go install github.com/weill-labs/amux@latest
```

On first server start, `amux` installs its `amux` terminfo entry into `~/.terminfo`.
This requires `tic` from ncurses. You can also run it explicitly:

```bash
amux install-terminfo
```

The `brew` and `curl` paths use GitHub Releases, so they become available once a tagged release is published.

## Quick Start

**Human**

```bash
# Start or reattach to the main session
amux

# Or create a named session
amux new my-project

# Or target an existing named session directly
amux -s my-project
```

**Agent**

```bash
# Inspect the session as structured JSON
amux capture --format json

# Send a command to a pane and wait for it to finish
amux send-keys pane-1 "make test" Enter
amux wait exited pane-1

# Hand a task to an agent pane once its screen goes quiet
amux wait idle pane-31 --timeout 30s
amux send-keys pane-31 "Fix the auth timeout bug" Enter

# Broadcast the same command to multiple panes
amux broadcast --panes pane-1,pane-2 "make test" Enter

# Subscribe to state changes
amux events --filter idle
```

See the [CLI Reference](#agent-api-1) for the full command surface.

## Agent API

Every operation is a single CLI call — no libraries, no SDK, language-agnostic.

### Structured Capture

Capture the full session state as structured JSON:

```bash
amux capture --format json
amux capture --history --format json
```

Full-session capture reads server-owned pane state by default and does not
require an attached interactive client. Use `--client` when you need the
attached client's displayed view, including client-local overlays.

Returns a JSON object with session metadata, window info, and per-pane state:

```json
{
  "session": "my-project",
  "window": {"id": 1, "name": "main", "index": 1},
  "width": 200, "height": 50,
  "panes": [
    {
      "id": 1,
      "name": "pane-1",
      "active": true,
      "zoomed": false,
      "host": "local",
      "task": "",
      "meta": {
        "prs": [42],
        "issues": ["LAB-338"]
      },
      "color": "f5e0dc",
      "position": {"x": 0, "y": 0, "width": 100, "height": 49},
      "cursor": {"col": 12, "row": 24, "hidden": false, "style": "block", "blinking": true},
      "terminal": {
        "alt_screen": false,
        "foreground_color": "ffffff",
        "background_color": "000000",
        "cursor_color": "ffffff",
        "mouse": {"tracking": "none", "sgr": false},
        "palette": ["000000", "800000", "008000", "808000", "000080", "800080", "008080", "c0c0c0"]
      },
      "content": ["$ make test", "PASS", "ok  github.com/weill-labs/amux 5.432s", "$ ▊"],
      "exited": true,
      "exited_since": "2025-06-15T10:30:01Z",
      "idle": true,
      "idle_since": "2025-06-15T10:30:00Z",
      "current_command": "bash",
      "last_output": "2025-06-15T10:29:58Z"
    }
  ]
}
```

Examples abbreviate `terminal.palette`; real output always includes all 256 entries in stable ANSI index order. Pane JSON also carries a nested `meta` object (raw kv store plus `task`, `git_branch`, `pr`, `tracked_prs`, `tracked_issues`); the legacy top-level `task`, `git_branch`, and `pr` fields remain for compatibility. Capture JSON is additive — ignore unknown fields so future releases can extend the schema without breaking parsers.

Single-pane, history, and per-pane scrollback variants follow the same pattern:

```bash
amux capture --format json pane-1     # one pane
amux capture --history pane-1         # retained scrollback + visible screen
amux capture --history --rewrap 120 pane-1   # reflow narrow rows to a wider width
```

`--history` prepends each pane's retained scrollback; `--rewrap` best-effort reflows narrow-pane rows to a wider width (exact for rows captured live, best-effort for restored history). amux retains `scrollback_lines` per pane (default `5000`). The [CLI Reference](#agent-api-1) lists every capture variant, and [docs/capture-architecture.md](docs/capture-architecture.md) covers the server-side vs client-side (`--client`) capture model.

Because retained history is server-owned, `capture --history` works after detach/reattach, after `reload-server`, and after crash recovery, and it does not require an attached interactive client. Copy mode remains per-client UI state over that shared history.

### Wait Commands

Block until a condition is met — no polling. The signals come in two families:
**output quiescence** (`wait idle` — the pane's screen output has settled for
`--settle`, default `2s`) and **foreground process** (`wait exited` / `wait busy`,
read from the terminal's foreground process group). `wait ready` requires both;
`wait content` waits for a substring; and `wait layout` / `wait clipboard` /
`wait checkpoint` / `wait ui` block on generation counters. All accept
`--timeout`. See the [CLI Reference](#agent-api-1) for the full list with default
timeouts.

### Event Stream

Subscribe to real-time session events as NDJSON:

```bash
amux events [--filter layout,idle,busy,exited,client-connect,client-disconnect,display-panes-shown,choose-window-shown] [--pane pane-1] [--client client-1] [--throttle 50ms] [--no-reconnect]
```

Use `amux list-clients` to discover attached client IDs for `send-keys --client`, `--client` event filters, and `wait ui`.

```json
{"type":"layout","ts":"2025-06-15T10:30:00.123Z","generation":42,"active_pane":"pane-1"}
{"type":"terminal","ts":"2025-06-15T10:30:00.200Z","pane_id":1,"pane_name":"pane-1","host":"local","cursor":{"col":12,"row":24,"hidden":false,"style":"bar","blinking":false},"terminal":{"alt_screen":true,"foreground_color":"112233","background_color":"445566","cursor_color":"778899","hyperlink":{"url":"https://example.com"},"mouse":{"tracking":"none","sgr":false},"palette":["000000","800000","008000","808000","000080","800080","008080","c0c0c0"]}}
{"type":"idle","ts":"2025-06-15T10:30:01.456Z","pane_id":2,"pane_name":"pane-2","host":"lambda-a100"}
{"type":"busy","ts":"2025-06-15T10:30:05.789Z","pane_id":2,"pane_name":"pane-2","host":"lambda-a100"}
{"type":"exited","ts":"2025-06-15T10:30:07.850Z","pane_id":2,"pane_name":"pane-2","host":"lambda-a100"}
{"type":"client-connect","ts":"2025-06-15T10:30:05.900Z","client_id":"client-2"}
{"type":"client-disconnect","ts":"2025-06-15T10:30:06.000Z","client_id":"client-2","reason":"explicit-detach"}
{"type":"reconnect","ts":"2025-06-15T10:30:06.000Z"}
```

The terminal-event example above abbreviates `terminal.palette`; the real event payload always includes all 256 entries.

Event types: `layout`, `output`, `terminal`, `idle`, `busy`, `exited`, `client-connect`, `client-disconnect`, plus a client-generated `reconnect`. `idle`/`busy` are screen-quiet transitions; `exited` is the process-based signal that no foreground process remains; `terminal` fires when preserved pane metadata changes (cursor style, colors, hyperlink, alt-screen, palette). New subscribers receive the current state as an initial snapshot (including attached clients as `client-connect` events), so nothing is missed between subscribe and the first event. Output events are throttled per pane (`--throttle`, default 50ms; `0s` disables); other events pass through immediately. The stream auto-reconnects with backoff unless `--no-reconnect` is set.

### Pane Mailbox

`amux msg` is an out-of-band mailbox for panes. Sending a message stores it in
the amux server and records delivery state; it does not write to the
recipient's PTY, change focus, or interrupt whatever is running. A human in
another terminal or any agent or process can use it when pane-to-pane
coordination needs an explicit unread/read/ack flow instead of keystrokes.

Send a message with an explicit sender and one or more recipients:

```bash
amux msg send --from pane-1 --to pane-2 \
  --subject "Logs ready" \
  --body "The latest run is in /tmp/run.log."

printf 'multi-line body\n' | amux msg send --from pane-1 --to pane-2 --subject "Report"
amux msg send --from pane-1 --to pane-2 --body-file /tmp/message.txt --format json
```

Recipients check for unread summaries:

```bash
amux msg inbox pane-2 --unread
amux msg inbox pane-2 --unread --format json
amux msg drain-status pane-2 --format json
```

`inbox` output is summary-only: message ID, sender, subject, timestamps, ack
state, and body size. It does not include message bodies or metadata values.
This is the mailbox's notification boundary: a recipient must check its mailbox
with `msg inbox --unread` or an equivalent loop, because amux does not
automatically type into the pane.

After a message is read or acked by that recipient, it no longer appears in
`--unread`; use `msg inbox` without `--unread` to see retained deliveries. Pane
status lines may also show a compact `msg:N` unread badge when there is room,
but the badge is only a summary cue.

`msg drain-status` is the automation-oriented read+ack gate. Text output is a
bare pending count; JSON includes `unread`, `unacked`, `pending`,
`pending_fingerprint`, `pending_ids`, and a bounded `latest` summary list.
`pending` means a delivery still needs read or ack, so a read-but-unacked
message remains pending even though it no longer appears in `inbox --unread`.
The fingerprint changes when the pending delivery IDs or their read/ack-needed
state changes, which lets stop-hook integrations nudge once per distinct
pending state.
See [docs/integrations/mailbox-drain-gate.md](docs/integrations/mailbox-drain-gate.md)
for the Claude Code and Codex Stop-hook recipes.

Read and ack are separate. `read` returns the body and marks that delivery read
for the recipient, unless `--peek` is set. `ack` records an explicit
acknowledgement; `--status` is a generic token, commonly `seen`, `ok`, or
`error`.

```bash
amux msg read msg-000001 --for pane-2
amux msg read msg-000001 --for pane-2 --peek --format json
amux msg ack msg-000001 --for pane-2 --status seen
amux msg ack msg-000001 --for pane-2 --status error --note "Need the full log."
```

Reply with `msg reply` when the current pane is responding to a message it
received. It infers the original sender as the recipient, links the reply into
the original thread, and inherits the original topics and groups unless you
override them. Use `--ack` when the reply should also mark the original delivery
handled for the replying pane.

```bash
amux msg reply msg-000001 --from pane-2 --body "Saw it; please include stderr too."
amux msg reply msg-000001 --from pane-2 --body "Done" --ack ok --ack-note "handled"
```

When a command is run from an attached pane context, amux can default omitted
sender or target flags to that actor pane. For scripts and humans running from
ordinary terminals, pass `--from` on `send` and `--for` on `read`/`ack` so
delivery state is unambiguous.

### Agent Loop Example

```bash
#!/usr/bin/env bash
# Agent loop: run a command, wait for it to finish, inspect the result.

PANE="pane-1"

# 1. Send a command to the pane
amux send-keys "$PANE" "make test" Enter

# 2. Wait for the command to start (pane becomes busy)
amux wait busy "$PANE" --timeout 5s

# 3. Wait for it to finish (pane exits back to its shell)
amux wait exited "$PANE" --timeout 120s

# 4. Capture the result as structured JSON
output=$(amux capture --format json "$PANE")

# 5. Parse with jq and decide what to do next
exit_line=$(echo "$output" | jq -r '.panes[0].content[-2]')
if echo "$exit_line" | grep -q "FAIL"; then
  echo "Tests failed — reading output for diagnostics"
  echo "$output" | jq -r '.panes[0].content[]'
else
  echo "Tests passed"
fi
```

## Why amux?

**Why not tmux + scripts?**
`tmux capture-pane` returns raw text with ANSI escape codes. Parsing it requires regex heuristics that break across terminal widths and applications. amux returns structured JSON with metadata (idle state, cursor position, process info, layout coordinates).

**Why not tmux control mode?**
Control mode still delivers raw pane content and requires polling. amux has blocking waits (`wait idle`, `wait content`) and push-based events — an agent subscribes once and reacts to state changes without polling.

**Why not headless (expect/pexpect)?**
Headless tools cut the human out of the loop. Humans and agents work better on a shared screen. Both see the same panes, both can act on them.

**Does amux support all tmux features?**
No, and it doesn't aim to. amux implements what matters for human+agent pairing: splits, windows, zoom, searchable choosers, and the agent API. If you need tmux's full feature set (session groups, advanced hooks), use tmux.

## CLI Reference

All commands accept `-s <session>` to target a specific session. Panes are referenced by name (`pane-1`) or numeric ID (`1`). Prefix matches are also supported.
The public CLI keeps one command path per concept: target sessions with `-s`, create panes with `spawn`, inspect history with `log`, and reorder layouts with `move` or `swap`.

### Session

| Command | Description |
|---------|-------------|
| `amux` | Start or attach to the selected session (`main` by default) |
| `amux new [name]` | Start or attach to a named session |
| `amux status` | Show pane/window summary |
| `amux version` | Show build version |
| `amux reload-server` | Hot-reload the server (preserves panes) |

### Pane Management

| Command | Description |
|---------|-------------|
| `amux list [--no-cwd] [--json]` | List panes with metadata (including cwd by default) |
| `amux spawn [--auto] [--at <pane>] [--window <name\|id>] [--vertical\|--horizontal] [--root] [--focus] [--name NAME] [--task TASK] [--color COLOR]` | Create a new pane using default spawn, column-fill auto spawn, or targeted split placement |
| `amux focus <pane\|direction>` | Focus by name, ID, or direction (left/right/up/down/next) |
| `amux zoom [pane]` | Toggle zoom on a pane |
| `amux kill [pane]` | Kill a pane (default: active) |
| `amux send-keys (<pane>\|--window <index\|name>) [--via pty\|client] [--client <id>] [--wait ready\|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>...` | Send keystrokes to a pane |
| `amux broadcast (--panes <pane,pane,...> \| --window <index\|name> \| --match <glob>) [--hex] <keys>...` | Send the same keystrokes to multiple panes |
| `amux swap <p1> <p2> [--tree]` | Swap two panes, or their root-level groups with `--tree` |
| `amux swap forward\|backward` | Swap active pane with neighbor |
| `amux move <pane> up\|down` | Move a pane one slot within its split group |
| `amux move <pane> --before\|--after <target>` | Move a pane before or after another, reordering siblings when they share a split group |
| `amux move <pane> --to-column <target>` | Move one pane into the target pane's column, appending at the bottom |
| `amux rotate [--reverse]` | Rotate pane positions |
| `amux equalize [--vertical\|--all]` | Rebalance root columns, rows within columns, or both |
| `amux respawn <pane>` | Restart a local pane with a fresh shell in the same slot |
| `amux copy-mode [pane] [--wait ui=copy-mode-shown] [--timeout <duration>]` | Enter copy/scroll mode |
| `amux lead [pane]` / `amux lead --clear` | Set or clear the lead pane |
| `amux meta set <pane> key=value...` | Set pane metadata |
| `amux meta get <pane> [key]` | Read pane metadata |
| `amux meta rm <pane> key...` | Remove pane metadata keys |
`move` first checks whether both panes are siblings in the same split group. When they are, it reorders only that group. Otherwise it falls back to the existing root-level-group behavior, so moving `pane-3` can still move an entire column or row when the panes are in different branches.
`move up` and `move down` are shorthand for nudging a pane one slot earlier or later within its current split group.
`move --to-column` instead moves exactly one pane into the target pane's logical column and appends it to the bottom of that stack.
`spawn` is the canonical pane-creation command. Use plain `spawn` for the default vertical split path, `spawn --auto` for column-fill placement that rebalances after each insert, `spawn --window ...` to target another window's active pane, and `spawn --auto --window ...` to run auto-placement in that window. Use `spawn --at ...` for targeted pane splits. These layout mutations keep focus unless you add `--focus`. When the active pane is zoomed, they preserve the zoom and keep the focused pane unchanged unless `--focus` is set.
Higher-level prompt delegation now lives at the script layer: compose `wait idle`, `send-keys`, `wait busy`, and `wait exited` or `wait ready` to match the workflow you want.

### Agent API

| Command | Description |
|---------|-------------|
| `amux capture [pane]` | Capture screen output (text) |
| `amux capture --client [pane]` | Capture the attached client's displayed screen |
| `amux capture --history <pane>` | Capture retained scrollback plus visible screen |
| `amux capture --history --rewrap <width> <pane>` | Best-effort rewrap retained history and visible content to a wider width |
| `amux capture --format json [pane]` | Structured JSON capture |
| `amux capture --history --format json` | Full-session JSON with per-pane scrollback prepended to `content` |
| `amux capture --history --format json <pane>` | Pane JSON with separate `history` and visible `content` |
| `amux capture --history --rewrap <width> --format json <pane>` | Pane JSON rewrapped to the requested width |
| `amux capture --ansi [pane]` | Capture with ANSI escape codes |
| `amux capture --colors` | Capture border color map |
| `amux wait idle <pane> [--settle 2s] [--timeout 60s]` | Block until pane VT output quiesces |
| `amux wait ready <pane> [--timeout 10s]` | Block until pane VT output settles and no foreground process remains |
| `amux wait exited <pane> [--timeout 5s]` | Block until pane has no foreground process |
| `amux wait busy <pane> [--timeout 5s]` | Block until pane has a foreground process |
| `amux wait content <pane> <substring> [--timeout 10s]` | Block until substring appears in pane |
| `amux wait layout [--after N] [--timeout 3s]` | Block until layout generation > N |
| `amux wait clipboard [--after N] [--timeout 3s]` | Block until clipboard content changes |
| `amux wait checkpoint [--after N] [--timeout 15s]` | Block until a crash checkpoint write completes |
| `amux wait ui <event> [--client id] [--after N] [--timeout 5s]` | Block until a client-local UI state is reached |
| `amux cursor ui [--client id]` | Show current client UI generation counter |
| `amux cursor layout` | Show current layout generation counter |
| `amux cursor clipboard` | Show current clipboard generation counter |
| `amux events [--filter type,...] [--pane ref] [--client id] [--throttle 50ms] [--no-reconnect]` | Stream events as NDJSON (output throttled, auto-reconnect by default) |
| `amux list-clients` | List attached clients and client-local UI state |
| `amux log clients` | Show recent client attach/detach history |
| `amux log panes` | Show recent pane create/exit history with exit cwd, git branch, and reason |
| `amux msg send [--from <pane>] --to <pane[,pane...]> [--subject text] [--topic name] [--group name] [--metadata json] [--reply-to msg-id] (--body text\|--body-file path\|stdin) [--format json]` | Send an out-of-band mailbox message to panes |
| `amux msg reply <msg-id> [--from <pane>] [--to <pane[,pane...]>] [--subject text] [--topic name] [--group name] [--metadata json] [--ack ok\|error\|seen] [--ack-note text] (--body text\|--body-file path\|stdin) [--format json]` | Reply to a mailbox message, inferring the original sender when `--to` is omitted |
| `amux msg inbox [pane] [--unread] [--format json]` | List mailbox summaries, optionally unread only |
| `amux msg drain-status [pane] [--format json]` | Count unread-or-unacked mailbox work for stop-hook integrations |
| `amux msg read <msg-id> [--for pane] [--peek] [--format json]` | Return a message body for a recipient and mark it read unless `--peek` is set |
| `amux msg ack <msg-id> [--for pane] [--status ok\|error\|seen] [--note text] [--format json]` | Acknowledge a message for a recipient |

### Windows

| Command | Description |
|---------|-------------|
| `amux new-window [--name NAME]` | Create a new window |
| `amux close-window` | Close the active window |
| `amux list-windows` | List all windows |
| `amux select-window <index\|name>` | Switch to a window |
| `amux next-window` | Switch to next window |
| `amux prev-window` | Switch to previous window |
| `amux last-window` | Switch to the previously active window |
| `amux rename-window <name>` | Rename the active window |
| `amux resize-window <cols> <rows>` | Resize window to given dimensions |

### Remote (federation)

Mirror panes and windows from another amux server over SSH. The local server
dials `ssh <target>` and connects to the remote's Unix socket; mirrored panes
stream the remote PTY output and forward input back.

| Command | Description |
|---------|-------------|
| `amux remote add <name> --ssh <target> --socket <path> [--session <name>]` | Register a remote host |
| `amux remote list` | List registered remotes and health |
| `amux remote rm <name>` | Remove a remote |
| `amux remote status` | Show active mirrors per host |
| `amux remote panes <name>` | List a remote host's panes |
| `amux remote windows <name>` | List a remote host's windows |
| `amux remote attach <name>:<pane>` | Mirror a single remote pane locally |
| `amux remote attach-window <name>:<window>` | Mirror a whole remote window into a new local window |
| `amux remote detach <local-pane>` | Stop mirroring a pane |
| `amux remote detach-window <local-window>` | Tear down a mirrored window |
| `amux remote resize <local-pane>` | Resize the remote pane to match the local mirror |

A window mirror reconstructs the remote window's split layout, tracks structural
changes live (panes added/removed/re-split), and pushes the local window's size
to the remote so it re-renders to match — most effective for headless remote
windows. Mirrors survive server reloads.

## Keybindings

Default prefix: `Ctrl-a`.

| Key | Action |
|-----|--------|
| `Ctrl-a \` | Root-level split left/right |
| `Ctrl-a -` | Split active pane top/bottom |
| `Ctrl-a \|` | Split active pane left/right |
| `Ctrl-a _` | Root-level split top/bottom |
| `Ctrl-a a` | Spawn pane in column-fill order |
| `Ctrl-a x` | Kill active pane |
| `Ctrl-a X` | Close active window |
| `Ctrl-a z` | Toggle zoom on active pane |
| `Ctrl-a }` / `Ctrl-a {` | Swap active pane with next/previous |
| `Ctrl-a o` | Cycle focus to next pane |
| `Ctrl-a h/j/k/l` | Focus left/down/up/right |
| `Ctrl-a arrow keys` | Focus in arrow direction |
| `Alt-h/j/k/l` | Focus left/down/up/right (no prefix) |
| `Ctrl-a H/J/K/L` | Resize pane left/down/up/right |
| `Ctrl-a =` | Equalize root column widths |
| `Ctrl-a [` | Enter copy/scroll mode |
| `Ctrl-a c` | Create new window |
| `Ctrl-a n` / `Ctrl-a p` | Next/previous window |
| `Ctrl-a ;` | Last active window |
| `Ctrl-a .` | Rename active pane |
| `Ctrl-a q` | Show pane labels for quick jump |
| `Ctrl-a ?` | Toggle keybinding help bar |
| `Ctrl-a 1-9` | Select window by number |
| `Ctrl-a r` | Hot reload (re-exec binary) |
| `Ctrl-a d` | Detach from session |
| `Ctrl-a Ctrl-a` | Send literal Ctrl-a |

## Configuration

Config file: `~/.config/amux/config.toml` (or set `AMUX_CONFIG` env var).

Theme icon modes, status styles, Nerd Font caveats, and fallback guidance are
covered in [docs/themes.md](docs/themes.md).

For attach-time terminal capability negotiation, you can override auto-detection
with `AMUX_CLIENT_CAPABILITIES`. Use a comma-separated list of capability names:
`kitty_keyboard`, `hyperlinks`, `rich_underline`, `cursor_metadata`,
`prompt_markers`, `graphics_placeholder`. Special values: `all`,
`legacy`/`none`, and `-name` or `!name` to disable a specific capability.

### Session

```toml
scrollback_lines = 5000    # optional: retained history per pane (default: 5000, must be >= 1)
```

### Debugging

```toml
[debug]
pprof = true               # optional: expose net/http/pprof on a per-session Unix socket
```

When enabled, the server listens on `/tmp/amux-$UID/<session>.pprof` with mode `0600`
and serves the standard `net/http/pprof` handlers over that Unix socket.

Interactive clients also publish pprof on per-process sockets at
`/tmp/amux-$UID/<session>.client.<pid>.pprof`. The most recently attached client
is aliased at `/tmp/amux-$UID/<session>.client.pprof`, which is what the CLI
debug wrappers use.

Useful wrappers:

```bash
amux debug dump
amux debug goroutines
amux debug goroutines --summary
amux debug heap
amux debug heap --raw > heap.pprof
amux debug profile --duration 30s > cpu.pprof.gz
amux debug info
amux debug socket
amux debug client-goroutines
amux debug client-heap
amux debug client-profile --duration 30s > client-cpu.pprof.gz
```

The old `amux _diag` entrypoint is deprecated. `amux _diag dump` and
`amux _diag heap` remain as compatibility aliases during the deprecation window;
new scripts should use `amux debug dump` and `amux debug heap --raw`.

### MCP Mailbox Server

`amux mcp-server` runs a local MCP server over stdio for the selected amux
session. Configure MCP clients that accept command/args servers with:

```json
{
  "mcpServers": {
    "amux": {
      "command": "amux",
      "args": ["-s", "main", "mcp-server"]
    }
  }
}
```

The server exposes pane mailbox tools for sending messages, listing an inbox,
reading a message, acknowledging a delivery, and waiting for the next matching
message. The tools use the same mailbox semantics as `amux msg` and `amux wait
msg`, including pane references by name, numeric ID, or prefix.

## AI Agent Support

Shared repo guidance lives in [AGENTS.md](AGENTS.md). This is the instruction file for coding agents in this repo.

- Claude Code also loads repo automation from `.claude/settings.json` and `.claude/hooks/`.
- Codex reads `AGENTS.md` and can discover repo skills from `.agents/skills/`.
- `make setup` installs the repo Git hooks for everyone. It is not Claude-specific.
- In an amux pane, prefer `scripts/gh-pr-create.sh ...` when opening a PR; with repo hooks active, later `git push` calls re-sync pane PR metadata for any agent.
- Optional for Codex users: trust the repo, then install the OpenAI Docs MCP server with `codex mcp add openaiDeveloperDocs --url https://developers.openai.com/mcp`.

## License

MIT
