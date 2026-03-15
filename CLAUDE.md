# CLAUDE.md

## Design Philosophy

amux is a **standalone terminal multiplexer for the human+agent workflow**. Single Go binary, no tmux dependency. It manages panes, layout, and rendering natively via a client-server architecture over Unix sockets.

### Core Principles

**Build on existing habits.** amux feels familiar to tmux users. `amux` starts a session. `Ctrl-a \` splits. Panes are auto-tagged. The muscle memory transfers.

**Client-server architecture.** The server is a background daemon that owns PTYs and layout state. Clients connect over a Unix socket, receive layout snapshots and raw pane output, and render locally. This enables hot-reload: rebuilding the binary auto-restarts the client with new rendering code while preserving running shells.

**Pane metadata is in-memory.** All pane state lives in `mux.PaneMeta` structs on the server. No external database or state files.

**Names over IDs.** Users reference panes by name (`pane-3`) or numeric ID (`3`). `Window.ResolvePane()` handles resolution. Prefix matches are also supported.

## Architecture

```
main.go                       CLI dispatch, client attach loop, keybinding handling
client.go                     ClientRenderer — client-side rendering with local vt emulators
reload.go                     Hot-reload: watches binary, re-execs client on change
internal/
  mux/
    layout.go                 LayoutCell tree, split/close/resize, proportional sizing
    window.go                 Window (layout + active pane), Focus(), Minimize/Restore
    pane.go                   Pane struct, PTY management, PaneMeta
    emulator.go               VT terminal emulator wrapper (vt100 lib)
    snapshot.go               LayoutSnapshot serialization for wire protocol
  server/
    server.go                 Server + Session structs, socket listener, attach/detach
    client_conn.go            Per-client connection, command dispatch (list/split/focus/etc.)
    protocol.go               Wire protocol: Message types, gob encoding over Unix socket
  render/
    compositor.go             RenderFull() — composites panes, borders, status bars
    border.go                 Border map, junction characters, active-pane coloring
    statusbar.go              Per-pane status lines, global session bar
    ansi.go                   ANSI escape sequences, Catppuccin Mocha palette
    panedata.go               PaneData interface for rendering
  proto/
    types.go                  Shared types (LayoutSnapshot, CellSnapshot, PaneSnapshot)
  config/
    config.go                 ~/.config/amux/hosts.toml parsing
test/
  harness_test.go             Integration test harness (drives amux inside a real tmux session)
  amux_test.go                Integration tests (~30 tests)
```

### Key Abstractions

**Client-server protocol** — Clients send `MsgTypeInput`, `MsgTypeResize`, `MsgTypeCommand`. Server sends `MsgTypePaneOutput` (raw PTY bytes per pane), `MsgTypeLayout` (layout tree snapshot), `MsgTypeRender` (legacy pre-rendered ANSI).

**`mux.Window`** — Owns the layout tree (`LayoutCell`) and active pane. All layout operations (split, close, resize, focus) go through Window methods.

**`mux.LayoutCell`** — Binary tree of splits. Leaves hold panes. Internal nodes hold a split direction and children. `Walk()` for traversal, `FindPane()` for lookup, `FixOffsets()` after structural changes.

**`Window.ResolvePane(ref)`** — Accepts pane name (`pane-1`), numeric ID (`1`), or prefix match. All CLI commands route through this.

**`render.RenderFull()`** — Composites pane content, borders with junction characters, per-pane status lines, and the global session bar into a single ANSI output string.

### Patterns to Follow

**One package per concern.** Layout logic in `mux/`, rendering in `render/`, server protocol in `server/`. Packages depend on interfaces and shared types (`proto/`), not on each other's internals.

**Unit tests for layout/rendering logic.** See `layout_test.go`, `window_test.go`, `emulator_test.go`. Use `fakePaneID()` helper to create minimal panes for testing.

**Integration tests for end-to-end behavior.** The harness in `test/harness_test.go` runs amux inside a real tmux session, sends keys via `tmux send-keys`, and asserts on screen content via `tmux capture-pane`. Tests run in ~6s total.

**Guard against impossible states.** Minimize checks that at least one pane stays non-minimized. Restore caps height at available space. Focus fallback finds nearest pane when strict overlap matching fails.

## Development

### Build and Test

```bash
go build -o ~/.local/bin/amux .    # build + install (client hot-reloads automatically)
go test ./...                       # run all tests
```

### Testing Live

```bash
amux                    # start a session (or reattach to existing)
# Ctrl-a \ to split, then:
amux list               # verify panes
amux focus pane-3       # focus by name
amux capture            # capture full composited screen
amux capture pane-1     # capture single pane output
amux capture --ansi     # capture with ANSI color codes
amux minimize pane-2    # minimize by name
amux restore pane-2     # restore
```

### TDD Workflow

All development follows test-driven development: write a failing test first, then implement. The integration test harness makes this fast (~6s for the full suite).

### Test Philosophy

Tests should read like specs. Minimize logic in assertions so a human can read the test and immediately understand what behavior is expected. Prefer golden file comparisons (`assertGolden`) over inline predicate functions — the golden file *is* the spec, viewable as a standalone document.

**Golden files** live in `test/testdata/`. Two types:
- `.golden` — structural layout frame (status lines, borders, global bar). Open one and you see the expected screen layout.
- `.color` — border color map using Catppuccin color initials (`R`=Rosewater, `F`=Flamingo, `M`=Mauve, `.`=dim, `|`=global bar). Shows which borders should be colored at a glance.

Regenerate goldens after intentional rendering changes: `cd test && go test -run TestGolden -update`

### Post-PR Review

After creating a PR, always run the code review and code simplifier agents before considering the work done. These catch issues that are easy to miss during implementation: style inconsistencies, unnecessary complexity, and subtle bugs.

### Adding a New Feature

1. **Write an integration test first.** Add a test to `test/amux_test.go` that exercises the feature end-to-end via the tmux harness. Follow existing test patterns.
2. Implement the feature.
3. Verify the integration test passes: `go test -v -run TestYourFeature ./test/ -timeout 30s`
4. Add unit tests for complex logic (layout algorithms, rendering, protocol encoding).

### Fixing a Bug

1. **Write a failing regression test first.** Add a test to `test/amux_test.go` that reproduces the bug (it should fail before the fix).
2. Fix the bug.
3. Verify the test passes: `go test ./...`

### Adding a New CLI Command

1. Add the command name to the `switch` in `main.go` (use `runServerCommand()` for server-side commands)
2. Add the handler in `internal/server/client_conn.go` `handleCommand()` method
3. Update `printUsage()` in `main.go`
4. Write integration test in `test/amux_test.go`

### Server Restart vs Client Hot-Reload

The client watches the binary and re-execs itself on changes (`reload.go`). This means rendering changes take effect immediately after `go build`. However, **server-side changes** (protocol, session logic, pane management) require killing the server process:

```bash
# Find and kill the server
ps aux | grep 'amux _server'
kill <PID>
# Then: amux (starts fresh server with new binary)
```

Socket location: `/tmp/amux-$UID/<session-name>`

## Configuration

Config file: `~/.config/amux/hosts.toml`

```toml
[hosts.lambda-a100]
type = "remote"
user = "ubuntu"
address = "150.136.64.231"
project_dir = "~/Project"
gpu = "A100"
color = "f38ba8"    # optional, auto-assigned from Catppuccin Mocha palette

[hosts.macbook]
type = "local"
color = "a6e3a1"
```

## Issue Tracking

All issues and feature requests go in the Linear project (not GitHub Issues):
https://linear.app/weill-labs/project/amux-b3a52334f77c

Team key: `LAB`. Use the Linear skill to create/query issues.
