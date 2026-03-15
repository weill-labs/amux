# amux

[![CI](https://github.com/weill-labs/amux/actions/workflows/ci.yml/badge.svg)](https://github.com/weill-labs/amux/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/weill-labs/amux/graph/badge.svg?token=RY0CPn9v7g)](https://codecov.io/gh/weill-labs/amux)

A terminal multiplexer built for working alongside AI agents. Drop-in replacement for tmux — same muscle memory, new capabilities.

## Philosophy

1. **Agents are peers.** Keybindings for humans, CLI commands for agents — same panes, same capabilities.

2. **Zero friction.** Fast enough that neither human nor agent waits. Familiar enough that tmux users don't notice they've switched. Sessions survive upgrades so nothing is ever lost.

3. **One source of truth.** What the human sees on screen is exactly what the agent captures. No divergence, no stale state, no translation layer.

## Install

```bash
go install github.com/weill-labs/amux@latest
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
