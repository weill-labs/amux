# amux

[![CI](https://github.com/weill-labs/amux/actions/workflows/ci.yml/badge.svg)](https://github.com/weill-labs/amux/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/weill-labs/amux/graph/badge.svg?token=RY0CPn9v7g)](https://codecov.io/gh/weill-labs/amux)

A standalone terminal multiplexer for the human+agent workflow. Single Go binary, no tmux dependency.

amux manages panes, layout, and rendering natively via a client-server architecture over Unix sockets. It feels familiar to tmux users — `amux` starts a session, `Ctrl-a \` splits, panes are auto-tagged — but is built from the ground up for working alongside AI agents.

## Features

- **Client-server architecture** — server daemon owns PTYs and layout state; clients connect over Unix sockets and render locally
- **Hot-reload** — rebuild the binary and both client and server re-exec automatically, preserving all running shells
- **Pane management** — split, focus, minimize, restore, zoom, swap, rotate
- **Multiple windows** — create, switch, rename windows within a session
- **Copy/scroll mode** — scroll back through pane output
- **Named panes** — reference panes by name (`pane-1`) or ID (`1`) with prefix matching
- **Catppuccin Mocha theme** — color-coded borders per pane with active pane highlighting
- **Host configuration** — define local and remote hosts in `~/.config/amux/hosts.toml`

## Install

```bash
go install github.com/weill-labs/amux@latest
```

Or build from source:

```bash
git clone https://github.com/weill-labs/amux.git
cd amux
go build -o ~/.local/bin/amux .
```

## Quick Start

```bash
amux                        # start or reattach to a session
amux new my-project         # start a named session
amux -s my-project attach   # attach to a specific session
```

## Keybindings

| Key | Action |
|-----|--------|
| `Ctrl-a \` | Split active pane left/right |
| `Ctrl-a -` | Split active pane top/bottom |
| `Ctrl-a \|` | Root-level split left/right |
| `Ctrl-a _` | Root-level split top/bottom |
| `Ctrl-a z` | Toggle zoom on active pane |
| `Ctrl-a m` | Toggle minimize/restore |
| `Ctrl-a }` | Swap active pane with next |
| `Ctrl-a {` | Swap active pane with previous |
| `Ctrl-a o` | Cycle focus to next pane |
| `Ctrl-a h/j/k/l` | Focus left/down/up/right |
| `Ctrl-a H/J/K/L` | Resize pane left/down/up/right |
| `Ctrl-a [` | Enter copy/scroll mode |
| `Ctrl-a c` | Create new window |
| `Ctrl-a n/p` | Next/previous window |
| `Ctrl-a 1-9` | Select window by number |
| `Ctrl-a r` | Hot reload (re-exec binary) |
| `Ctrl-a d` | Detach from session |
| `Ctrl-a Ctrl-a` | Send literal Ctrl-a |

## CLI Commands

```
amux list                    List panes with metadata
amux status                  Show pane/window summary
amux focus <pane>            Focus a pane by name or ID
amux capture                 Capture full composited screen
amux capture <pane>          Capture a single pane's output
amux send-keys <pane> <keys> Send keystrokes to a pane
amux spawn --name NAME       Spawn a new agent pane
amux zoom [pane]             Toggle zoom on a pane
amux swap <p1> <p2>          Swap two panes
amux rotate [--reverse]      Rotate pane positions
amux minimize <pane>         Minimize a pane
amux restore <pane>          Restore a minimized pane
amux kill <pane>             Kill a pane
amux new-window              Create a new window
amux list-windows            List all windows
amux select-window <n>       Switch to window by index or name
amux reload-server           Hot-reload the server
amux version                 Show build version
```

Panes can be referenced by name (`pane-1`) or numeric ID (`1`). Prefix matches are supported.

## Configuration

Config file: `~/.config/amux/hosts.toml`

```toml
[hosts.lambda-a100]
type = "remote"
user = "ubuntu"
address = "150.136.64.231"
project_dir = "~/Project"
gpu = "A100"
color = "f38ba8"

[hosts.macbook]
type = "local"
color = "a6e3a1"
```

## Development

```bash
go build -o ~/.local/bin/amux .    # build + install (hot-reloads automatically)
go test ./...                       # run all tests
```

## License

MIT
