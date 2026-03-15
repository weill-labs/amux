# amux

[![CI](https://github.com/weill-labs/amux/actions/workflows/ci.yml/badge.svg)](https://github.com/weill-labs/amux/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/weill-labs/amux/graph/badge.svg?token=RY0CPn9v7g)](https://codecov.io/gh/weill-labs/amux)

A terminal multiplexer built for working alongside AI agents. Drop-in replacement for tmux — same muscle memory, new capabilities.

## Why amux

Terminal multiplexers haven't changed in decades, but the way we use terminals has. When you're running multiple AI agents alongside your own shell, you need panes that agents can discover, address by name, and programmatically control. amux makes that natural.

- **Agent-native CLI** — agents can `amux list`, `amux send-keys`, `amux capture`, and `amux spawn` without screen-scraping or brittle key sequences
- **Named panes** — reference panes by name (`pane-1`) or ID (`1`) instead of opaque indices
- **Hot-reload** — update amux without losing your running shells or pane layout
- **Multiple windows** — organize work across windows within a session
- **Catppuccin Mocha theme** — color-coded borders so you can tell panes apart at a glance

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
| `Ctrl-a }` / `{` | Swap active pane with next/previous |
| `Ctrl-a o` | Cycle focus to next pane |
| `Ctrl-a h/j/k/l` | Focus left/down/up/right |
| `Ctrl-a H/J/K/L` | Resize pane left/down/up/right |
| `Ctrl-a [` | Enter copy/scroll mode |
| `Ctrl-a c` | Create new window |
| `Ctrl-a n/p` | Next/previous window |
| `Ctrl-a 1-9` | Select window by number |
| `Ctrl-a r` | Hot reload |
| `Ctrl-a d` | Detach from session |
| `Ctrl-a Ctrl-a` | Send literal Ctrl-a |

## CLI

```
amux list                    List panes with metadata
amux status                  Show pane/window summary
amux focus <pane>            Focus a pane by name or ID
amux capture [pane]          Capture screen output (full or single pane)
amux send-keys <pane> <keys> Send keystrokes to a pane
amux spawn --name NAME       Spawn a new agent pane
amux zoom [pane]             Toggle zoom on a pane
amux swap <p1> <p2>          Swap two panes
amux rotate [--reverse]      Rotate pane positions
amux minimize <pane>         Minimize a pane
amux restore <pane>          Restore a minimized pane
amux kill <pane>             Kill a pane
amux new-window              Create a new window
amux select-window <n>       Switch to window by index or name
amux reload-server           Hot-reload the server
```

## Configuration

Host definitions live in `~/.config/amux/hosts.toml`:

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

## License

MIT
