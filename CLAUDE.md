# CLAUDE.md

## Design Philosophy

amux is **tmux reimagined for the human+agent workflow**. It builds on tmux and existing terminal habits, but fills gaps and absorbs functionality where the default tmux UX falls short — especially around managing multiple AI coding agents across local and remote machines.

### Core Principles

**Build on existing habits.** amux should feel familiar to tmux users. Typing `amux` starts a terminal session, not a dashboard. You get a shell. `Ctrl-\` splits. Panes are auto-tagged. The muscle memory transfers. Where tmux has sharp edges (pane IDs, no minimize, no metadata), amux smooths them out rather than asking users to learn a new paradigm.

**Absorb tmux where needed.** amux uses tmux as infrastructure but isn't afraid to own UX on top. When tmux's built-in behavior is adequate (scrollback, copy mode, mouse support), amux stays out of the way. When it's not (pane naming, minimize/restore, swap-with-metadata, agent status), amux provides a better experience directly. The boundary isn't "what tmux can do" — it's "what's most ergonomic."

**Pane metadata is the data model.** All pane state lives in tmux user options (`@amux_*`). No external database, no state files, no daemon. `tmux show-options -p` is the source of truth. This means state survives tmux detach/reattach, session save/restore, and amux restarts.

**Names over IDs.** Users think in names (`pane-3`, `auth-agent`), not tmux IDs (`%40`). Every command that takes a pane reference resolves names to IDs via `pane.ResolvePane()`. Raw tmux IDs (`%N`) are still accepted for scripting and tmux keybinding compatibility.

**Non-invasive.** Panes without `@amux_name` are invisible to amux. Regular tmux panes coexist with amux-managed panes. amux never touches panes it didn't create or tag.

## Architecture

Single Go binary. All tmux interaction abstracted behind the `tmux.Tmux` interface.

### Package Structure

```
main.go                    CLI dispatch — routes subcommands
internal/
  tmux/tmux.go             Tmux interface + LiveTmux implementation
  pane/pane.go             PaneInfo, Discover(), ResolvePane(), idle detection
  session/session.go       Session create/attach, configure keybindings/hooks
  minimize/minimize.go     Minimize (resize to 1) / restore (read saved height)
  swap/swap.go             Swap pane content + copy @amux_* metadata
  spawn/spawn.go           Local + remote agent spawning
  grid/                    Bubbletea TUI dashboard
    model.go               Model/Update/View
    keymap.go              Keybindings
    styles.go              Lipgloss styles, Catppuccin colors
  config/config.go         ~/.config/amux/hosts.toml parsing
```

### Key Abstractions

**`tmux.Tmux` interface** — Every package accepts this interface, never calls `exec.Command("tmux", ...)` directly. This enables unit testing with mock implementations. `LiveTmux` is the real implementation.

**`@amux_*` pane options** — The metadata namespace:
- `@amux_name` — display name (e.g., "pane-1", "auth-agent")
- `@amux_host` — "local" or hostname from config
- `@amux_task` — issue ID or description
- `@amux_remote` — remote tmux session name (SSH agents)
- `@amux_color` — hex color for pane border
- `@amux_minimized` — "1" when minimized
- `@amux_restore_h` — saved height before minimize

**`pane.ResolvePane()`** — Accepts a name string or raw tmux ID (`%N`). All CLI commands route through `resolveOrDie()` in main.go.

**`session.Start()`** — Uses `syscall.Exec` to replace the Go process with tmux. This is intentional — amux is a launcher, not a wrapper process that stays resident.

### Patterns to Follow

**One package per feature.** Minimize logic in `minimize/`, swap in `swap/`, etc. Each package depends on `tmux.Tmux` interface, not on other feature packages.

**Test with mock tmux.** Each test file creates a `mockTmux` struct implementing the `tmux.Tmux` interface. Tests never call real tmux. See `minimize/minimize_test.go` for the pattern.

**tmux swap-pane doesn't swap user options.** This is a tmux limitation. After `swap-pane`, amux must manually copy all `@amux_*` options between the two panes. The `swap.SwapWithMeta()` function handles this. Any new metadata keys must be added to `tmux.AmuxOptions`.

**Guard against impossible states.** Minimize checks that at least one pane stays non-minimized. Restore caps height at available space. These guards prevent the user from getting into an unrecoverable state without needing to drop to raw tmux.

## Development

### Build and Test

```bash
go build -o ~/.local/bin/amux .    # build + install
go test ./...                       # run all tests
```

### Testing Live

```bash
amux                    # start a session
# Ctrl-\ to split, then:
amux list               # verify panes are tagged
amux output pane-1      # read pane output by name
amux minimize pane-2    # minimize by name
amux restore pane-2     # restore
amux dashboard          # open TUI popup
```

### Adding a New Feature

1. Create `internal/<feature>/<feature>.go`
2. Accept `tmux.Tmux` interface as first parameter
3. Add mock-based tests in `<feature>_test.go`
4. Wire the subcommand in `main.go`
5. If the feature uses new `@amux_*` options, add them to `tmux.AmuxOptions`

### Adding New Metadata

1. Add the field to `tmux.PaneFields`
2. Add the format variable to `ListPanes()` format string
3. Parse it in the `ListPanes()` loop
4. Add the key to `tmux.AmuxOptions` (for swap metadata copy)
5. Expose it in `pane.PaneInfo` if the dashboard needs it

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

Linear project: https://linear.app/weill-labs/project/amux-b3a52334f77c
