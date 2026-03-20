# amux — terminal multiplexer for human+agent workflows

[![CI](https://github.com/weill-labs/amux/actions/workflows/ci.yml/badge.svg)](https://github.com/weill-labs/amux/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/weill-labs/amux/graph/badge.svg?token=RY0CPn9v7g)](https://codecov.io/gh/weill-labs/amux)

GUIs force screenshots and vision models. Headless APIs cut the human out.

**amux** is a shared TUI grid where humans use keybindings and agents use CLI commands. Same panes, same state.

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

Retained pane history is server-owned. Clients hydrate that history on attach and keep their own local copy-mode state (scroll position, search, selection) on top of it. That means history survives detach/reattach, hot reload, and crash recovery, while each viewer can still browse independently.

## Install

```bash
go install github.com/weill-labs/amux@latest
```

On first server start, `amux` installs its `amux` terminfo entry into `~/.terminfo`.
This requires `tic` from ncurses. You can also run it explicitly:

```bash
amux install-terminfo
```

## Quick Start

**Human**

```bash
# Start or reattach to the default session
amux

# Or create a named session
amux new my-project

# Or attach to an existing named session
amux -s my-project attach
```

**Agent**

```bash
# Inspect the current session
amux capture --format json

# Capture the full browsable buffer for one pane
amux capture --history pane-1

# Send a command to a pane and wait for it to finish
amux send-keys pane-1 "ls" Enter
amux wait-idle pane-1

# Subscribe to state changes
amux events --filter idle

# Discover attached clients
amux list-clients
```

## Agent API

Every operation is a single CLI call — no libraries, no SDK, language-agnostic.

### Structured Capture

Capture the full session state as structured JSON:

```bash
amux capture --format json
```

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
      "minimized": false,
      "zoomed": false,
      "host": "local",
      "task": "",
      "color": "f5e0dc",
      "position": {"x": 0, "y": 0, "width": 100, "height": 49},
      "cursor": {"col": 12, "row": 24, "hidden": false},
      "content": ["$ make test", "PASS", "ok  github.com/weill-labs/amux 5.432s", "$ ▊"],
      "idle": true,
      "idle_since": "2025-06-15T10:30:00Z",
      "current_command": "bash",
      "child_pids": []
    }
  ]
}
```

Capture a single pane:

```bash
amux capture --format json pane-1
```

History-aware pane capture:

```bash
amux capture pane-1
amux capture --history pane-1
amux capture --history --format json pane-1
```

`capture pane-1` returns the pane's current visible screen. `capture --history pane-1` returns the full browsable buffer for that pane: retained scrollback followed by the current screen. The JSON form keeps those separate as `history` and `content`.

Because retained history is server-owned, `capture --history` works after detach/reattach, after `reload-server`, and after crash recovery, and it does not require an attached interactive client. Copy mode remains per-client UI state over that shared history.

### Wait Commands

Block until a condition is met. No polling.

| Command | Description | Default timeout |
|---------|-------------|-----------------|
| `wait-idle <pane>` | Block until pane has no foreground process | 5s |
| `wait-busy <pane>` | Block until pane has a child process | 5s |
| `wait-for <pane> <substring>` | Block until substring appears in pane content | 10s |
| `wait-layout [--after N]` | Block until layout generation exceeds N | 3s |
| `wait-clipboard [--after N]` | Block until clipboard content changes | 3s |
| `wait-ui <event> [--client client-1]` | Block until a client-local UI state is reached | 5s |

All accept `--timeout <duration>` (e.g., `--timeout 30s`).

### Event Stream

Subscribe to real-time session events as NDJSON:

```bash
amux events [--filter layout,idle,busy,display-panes-shown,choose-window-shown] [--pane pane-1] [--host lambda-a100] [--client client-1]
```

Use `amux list-clients` to discover attached client IDs for `--client` and `wait-ui`.

```json
{"type":"layout","ts":"2025-06-15T10:30:00.123Z","generation":42,"active_pane":"pane-1"}
{"type":"idle","ts":"2025-06-15T10:30:01.456Z","pane_id":2,"pane_name":"pane-2","host":"lambda-a100"}
{"type":"busy","ts":"2025-06-15T10:30:05.789Z","pane_id":2,"pane_name":"pane-2","host":"lambda-a100"}
```

Event types: `layout`, `output`, `idle`, `busy`. New subscribers receive the current state as an initial snapshot, so no events are missed between subscribe and the first real event.

### Agent Loop Example

```bash
#!/usr/bin/env bash
# Agent loop: run a command, wait for it to finish, inspect the result.

PANE="pane-1"

# 1. Send a command to the pane
amux send-keys "$PANE" "make test" Enter

# 2. Wait for the command to start (pane becomes busy)
amux wait-busy "$PANE" --timeout 5s

# 3. Wait for it to finish (pane becomes idle)
amux wait-idle "$PANE" --timeout 120s

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
Control mode still delivers raw pane content and requires polling. amux has blocking waits (`wait-idle`, `wait-for`) and push-based events — an agent subscribes once and reacts to state changes without polling.

**Why not headless (expect/pexpect)?**
Headless tools cut the human out of the loop. Humans and agents work better on a shared screen. Both see the same panes, both can act on them.

**Does amux support all tmux features?**
No, and it doesn't aim to. amux implements what matters for human+agent pairing: splits, windows, zoom, minimize, remote hosts, searchable choosers, and the agent API. If you need tmux's full feature set (session groups, advanced hooks), use tmux.

## CLI Reference

All commands accept `-s <session>` to target a specific session. Panes are referenced by name (`pane-1`) or numeric ID (`1`). Prefix matches are also supported.

### Session

| Command | Description |
|---------|-------------|
| `amux` | Start or attach to default session |
| `amux new [name]` | Start a new named session |
| `amux attach [session]` | Attach to a session |
| `amux status` | Show pane/window summary |
| `amux version` | Show build version |
| `amux reload-server` | Hot-reload the server (preserves panes) |

### Pane Management

| Command | Description |
|---------|-------------|
| `amux list` | List panes with metadata |
| `amux split [--host HOST]` | Split active pane (default: horizontal) |
| `amux focus <pane\|direction>` | Focus by name, ID, or direction (left/right/up/down/next) |
| `amux spawn --name NAME [--host HOST] [--task TASK]` | Spawn a new named pane |
| `amux zoom [pane]` | Toggle zoom on a pane |
| `amux minimize <pane>` | Minimize a pane |
| `amux restore <pane>` | Restore a minimized pane |
| `amux kill [pane]` | Kill a pane (default: active) |
| `amux send-keys <pane> [--hex] <keys>...` | Send keystrokes to a pane |
| `amux swap <p1> <p2>` | Swap two panes |
| `amux swap forward\|backward` | Swap active pane with neighbor |
| `amux rotate [--reverse]` | Rotate pane positions |
| `amux copy-mode [pane]` | Enter copy/scroll mode |

### Agent API

| Command | Description |
|---------|-------------|
| `amux capture [pane]` | Capture screen output (text) |
| `amux capture --history <pane>` | Capture retained scrollback plus visible screen |
| `amux capture --format json [pane]` | Structured JSON capture |
| `amux capture --history --format json <pane>` | Pane JSON with separate `history` and visible `content` |
| `amux capture --ansi [pane]` | Capture with ANSI escape codes |
| `amux capture --colors` | Capture border color map |
| `amux wait-idle <pane> [--timeout 5s]` | Block until pane becomes idle |
| `amux wait-busy <pane> [--timeout 5s]` | Block until pane has child processes |
| `amux wait-for <pane> <substring> [--timeout 10s]` | Block until substring appears in pane |
| `amux wait-layout [--after N] [--timeout 3s]` | Block until layout generation > N |
| `amux wait-clipboard [--after N] [--timeout 3s]` | Block until clipboard content changes |
| `amux wait-ui <event> [--client id] [--timeout 5s]` | Block until a client-local UI state is reached |
| `amux generation` | Show current layout generation counter |
| `amux events [--filter type,...] [--pane ref] [--host name] [--client id]` | Stream events as NDJSON |
| `amux list-clients` | List attached clients and client-local UI state |

### Windows

| Command | Description |
|---------|-------------|
| `amux new-window [--name NAME]` | Create a new window |
| `amux list-windows` | List all windows |
| `amux select-window <index\|name>` | Switch to a window |
| `amux next-window` | Switch to next window |
| `amux prev-window` | Switch to previous window |
| `amux rename-window <name>` | Rename the active window |
| `amux resize-window <cols> <rows>` | Resize window to given dimensions |

### Remote Hosts

| Command | Description |
|---------|-------------|
| `amux hosts` | List configured remote hosts and connection status |
| `amux split --host HOST` | Split with a remote pane on HOST |
| `amux disconnect <host>` | Drop SSH connection to a host |
| `amux reconnect <host>` | Reconnect to a remote host |
| `amux unsplice <host>` | Revert SSH takeover, replace remote panes with local |

### Hooks

| Command | Description |
|---------|-------------|
| `amux set-hook <event> <command>` | Register a hook (events: `on-idle`, `on-activity`) |
| `amux unset-hook <event> [index]` | Remove hook(s) for an event |
| `amux list-hooks` | List registered hooks |

## Keybindings

Default prefix: `Ctrl-a`. Configurable via `~/.config/amux/config.toml` (see [Configuration](#configuration)).

| Key | Action |
|-----|--------|
| `Ctrl-a \` | Split active pane left/right |
| `Ctrl-a -` | Split active pane top/bottom |
| `Ctrl-a \|` | Root-level split left/right |
| `Ctrl-a _` | Root-level split top/bottom |
| `Ctrl-a x` | Kill active pane |
| `Ctrl-a z` | Toggle zoom on active pane |
| `Ctrl-a M` | Toggle minimize/restore |
| `Ctrl-a }` / `Ctrl-a {` | Swap active pane with next/previous |
| `Ctrl-a o` | Cycle focus to next pane |
| `Ctrl-a h/j/k/l` | Focus left/down/up/right |
| `Ctrl-a arrow keys` | Focus in arrow direction |
| `Alt-h/j/k/l` | Focus left/down/up/right (no prefix) |
| `Ctrl-a H/J/K/L` | Resize pane left/down/up/right |
| `Ctrl-a [` | Enter copy/scroll mode |
| `Ctrl-a c` | Create new window |
| `Ctrl-a n` / `Ctrl-a p` | Next/previous window |
| `Ctrl-a q` | Show pane labels for quick jump |
| `Ctrl-a 1-9` | Select window by number |
| `Ctrl-a r` | Hot reload (re-exec binary) |
| `Ctrl-a d` | Detach from session |
| `Ctrl-a Ctrl-a` | Send literal Ctrl-a |

## Configuration

Config file: `~/.config/amux/config.toml` (or set `AMUX_CONFIG` env var).

### Remote Hosts

```toml
[hosts.lambda-a100]
type = "remote"
user = "ubuntu"
address = "150.136.64.231"
identity_file = "~/.ssh/id_ed25519"
project_dir = "~/Project"
gpu = "A100"
color = "f38ba8"            # Catppuccin Red — optional, auto-assigned if omitted

[hosts.macbook]
type = "local"
color = "a6e3a1"            # Catppuccin Green
```

### Keybindings

```toml
[keys]
preset = "tmux"             # optional: start from the built-in tmux-compatible map
prefix = "C-b"              # change prefix to Ctrl-b (default: Ctrl-a)
unbind = ["M", "["]         # remove default bindings

[keys.bind]
"s" = "split v"             # bind Ctrl-b s to vertical split
"q" = "kill"                # bind Ctrl-b q to kill pane
"m" = "toggle-minimize"     # restore the pre-LAB-241 minimize key if desired
```

Key format: single character (`d`, `\\`, `-`) or Ctrl combo (`C-a`, `C-b`). Actions match CLI command names (e.g., `split`, `focus left`, `zoom`, `kill`).

Built-in presets:
- `amux` (default): the native amux keymap documented in `amux version`
- `tmux`: tmux-style prefix and bindings for supported features such as `%`, `"`, `q`, `s`, `w`, `c`, `n`, `p`, `[` and `Ctrl-o`

`prefix`, `bind`, and `unbind` still apply on top of a preset, so you can start from `tmux` and tweak from there.

## AI Agent Support

Shared repo guidance lives in [AGENTS.md](AGENTS.md). This is the instruction file for coding agents in this repo.

- Claude Code also loads repo automation from `.claude/settings.json` and `.claude/hooks/`.
- Codex reads `AGENTS.md` and can discover repo skills from `.agents/skills/`.
- `make setup` installs the repo Git hooks for everyone. It is not Claude-specific.
- Optional for Codex users: trust the repo, then install the OpenAI Docs MCP server with `codex mcp add openaiDeveloperDocs --url https://developers.openai.com/mcp`.

## License

MIT
