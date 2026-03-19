# Changelog

## v0.1.0

First tagged release.

### Agent API

- **Structured JSON capture** (`amux capture --format json`) — session metadata, per-pane content, cursor state, layout coordinates, and process info in one call.
- **Blocking wait commands** — `wait-idle`, `wait-busy`, `wait-for`, `wait-layout`, `wait-clipboard`, `wait-ui`. No polling required.
- **Push-based event stream** (`amux events`) — subscribe to `layout`, `output`, `idle`, `busy` events as NDJSON. New subscribers receive a state snapshot on connect.
- **`api_version` field** in JSON capture output for forward-compatible tooling.
- **Single-pane JSON capture** includes `position` field matching full-screen capture.
- **Agent status fields** — `idle`, `idle_since`, `current_command`, `child_pids` in capture output.
- **Client discovery** — `amux list-clients` for multi-client coordination.

### Terminal Multiplexer

- **Client-server architecture** — background daemon owns PTYs and layout state; clients connect over a Unix socket and render locally.
- **Hot-reload** — `make build` replaces the binary atomically; both client and server re-exec without losing panes or shells.
- **Session persistence** — detach with `Ctrl-a d`, reattach with `amux attach`.
- **Splits and windows** — vertical/horizontal splits, root-level splits, multiple windows with `new-window`/`select-window`.
- **Zoom and minimize** — `amux zoom` for full-screen focus, `amux minimize`/`restore` for collapsing panes.
- **Pane operations** — `spawn`, `kill`, `swap`, `rotate`, `send-keys`, `copy-mode`.
- **Mouse support** — click to focus, drag to resize borders, scroll wheel for copy mode.
- **Interactive choosers** — `Ctrl-a q` for pane labels, `Ctrl-a w` for window chooser with search.

### Remote Hosts

- **SSH pane hosting** — `amux split --host HOST` opens a pane on a remote machine via SSH.
- **Connection management** — `amux hosts`, `amux disconnect`, `amux reconnect`, `amux unsplice`.
- **Config-driven** — define hosts in `~/.config/amux/config.toml` with user, address, identity file, GPU label, and color.

### Configuration

- **Keybinding presets** — `amux` (default) and `tmux` presets, with per-key overrides via `config.toml`.
- **Custom prefix key** — change from `Ctrl-a` to any key combo.
- **Pane colors** — optional per-host colors from the Catppuccin Mocha palette, auto-assigned if omitted.

### Hooks

- **`set-hook`/`unset-hook`/`list-hooks`** — register shell commands on `on-idle` and `on-activity` events.

### Platform

- Cross-compiled for darwin/linux on amd64/arm64.
- Single binary, no runtime dependencies.
