# CLAUDE.md

## Design Philosophy

See [README.md — Philosophy](README.md#philosophy) for the project thesis and three tenets.

**Client-server architecture.** The server is a background daemon that owns PTYs and layout state. Clients connect over a Unix socket, receive layout snapshots and raw pane output, and render locally. This enables hot-reload: rebuilding the binary auto-restarts the client with new rendering code while preserving running shells.

**Pane metadata is in-memory.** All pane state lives in `mux.PaneMeta` structs on the server. No external database or state files.

**Names over IDs.** Users reference panes by name (`pane-3`) or numeric ID (`3`). `Window.ResolvePane()` handles resolution. Prefix matches are also supported.

## Architecture

### Key Abstractions

**Client-server protocol** — Clients send `MsgTypeInput`, `MsgTypeResize`, `MsgTypeCommand`. Server sends `MsgTypePaneOutput` (raw PTY bytes per pane), `MsgTypeLayout` (layout tree snapshot), `MsgTypeRender` (legacy pre-rendered ANSI).

**`mux.Window`** — Owns the layout tree (`LayoutCell`) and active pane. All layout operations (split, close, resize, focus) go through Window methods.

**`mux.LayoutCell`** — Binary tree of splits. Leaves hold panes. Internal nodes hold a split direction and children. `Walk()` for traversal, `FindPane()` for lookup, `FixOffsets()` after structural changes.

**`Window.ResolvePane(ref)`** — Accepts pane name (`pane-1`), numeric ID (`1`), or prefix match. All CLI commands route through this.

**`render.RenderFull()`** — Composites pane content, borders with junction characters, per-pane status lines, and the global session bar into a single ANSI output string.

### Patterns to Follow

**One package per concern.** Layout logic in `mux/`, rendering in `render/`, server protocol in `server/`. Packages depend on interfaces and shared types (`proto/`), not on each other's internals.

**Unit tests for layout/rendering logic.** See `layout_test.go`, `window_test.go`, `emulator_test.go`. Use `fakePaneID()` helper to create minimal panes for testing.

**Integration tests for end-to-end behavior.** The harness in `test/server_harness_test.go` drives amux directly over the Unix socket — no tmux dependency. Tests run in ~6s total.

**Guard against impossible states.** Minimize checks that at least one pane stays non-minimized. Restore caps height at available space. Focus fallback finds nearest pane when strict overlap matching fails.

**Minimize requires a horizontal split.** `Minimize` only works on panes in a horizontal split (`splitH` / top-bottom layout). Panes in a vertical split (`splitV` / left-right layout) cannot be minimized — the command returns an error. Tests that exercise minimize must use `splitH()`, not `splitV()`.

**Colors live in `config/config.go`.** The Catppuccin Mocha palette (`CatppuccinMocha`), letter abbreviations (`CatppuccinLetters`), and named hex constants (`DimColorHex`, `TextColorHex`) are defined once in the config package. Reference these constants instead of hardcoding hex values like `"f5e0dc"` or `"6c7086"`.

## Development

### Build and Test

```bash
make setup                         # after cloning: activate git hooks (formatting, lint, commit guards)
go build -o ~/.local/bin/amux .    # build + install (client hot-reloads automatically)
go test ./...                       # run all tests
```

### Testing Live

See [README.md — CLI](README.md#cli) for the full command reference. Key commands for testing:

```bash
amux                              # start or reattach
amux capture --format json        # structured JSON for agents
amux capture --format json pane-1 # single pane JSON
```

### TDD Workflow

All development follows test-driven development: write a failing test first, then implement. The integration test harness makes this fast (~6s for the full suite).

### Test Philosophy

Tests should read like specs. Minimize logic in assertions so a human can read the test and immediately understand what behavior is expected. Prefer golden file comparisons (`assertGolden`) over inline predicate functions — the golden file *is* the spec, viewable as a standalone document. Use table-driven tests for unit tests with multiple cases — define a `tests` slice of structs, iterate with `t.Run(tt.name, ...)`, and call `t.Parallel()` in each subtest.

**Golden files** live in `test/testdata/`. Two types:
- `.golden` — structural layout frame (status lines, borders, global bar). Open one and you see the expected screen layout.
- `.color` — border color map using Catppuccin color initials (`R`=Rosewater, `F`=Flamingo, `M`=Mauve, `.`=dim, `|`=global bar). Shows which borders should be colored at a glance.

Regenerate goldens after intentional rendering changes: `cd test && go test -run TestGolden -update`

### Pre-Push Rebase

Rebase onto `origin/main` before the first push (`git fetch origin main && git rebase origin/main`). Multiple features often land in parallel; rebasing before push avoids repeated merge conflict resolution after the PR is open.

### Specs and Plans on Feature Branches

Commit design specs and implementation plans to the feature branch, not main. Committing to main before creating the feature branch causes divergent branches on subsequent pulls.

### Post-PR Review

After creating a PR, immediately dispatch the code review and code simplifier agents (in background) before presenting the PR URL. Address their feedback before considering the work done. These catch issues that are easy to miss during implementation: style inconsistencies, unnecessary complexity, and subtle bugs.

### Include Baseline Numbers in Performance PRs

When creating PRs that add or modify benchmarks, include a "Baseline numbers" section in the PR description with representative results in a markdown table. Include the hardware (e.g., "Apple M4, macOS") for context. Development run results are ephemeral — the PR description is the permanent record.

### Adding a New Feature

See [CONTRIBUTING.md](CONTRIBUTING.md) for the workflow. Additional note for AI agents:

1. **Check what dependencies already provide.** Before designing a custom solution, check if the underlying library (e.g., `charmbracelet/x/vt`) already supports the capability. Read tests and exported methods in `go/pkg/mod/`.

### Fixing a Bug

See [CONTRIBUTING.md](CONTRIBUTING.md) for the workflow.

### Adding a New CLI Command

See [CONTRIBUTING.md](CONTRIBUTING.md) for the workflow.

### Hot-Reload

Both client and server watch the binary and re-exec on changes (`reload.go`). Running `go build -o ~/.local/bin/amux .` triggers automatic reload of both — panes and shells are preserved across server reloads via checkpoint/restore.

Socket location: `/tmp/amux-$UID/<session-name>`

## Configuration

See [README.md — Configuration](README.md#configuration) for the `hosts.toml` format. Config parsing lives in `config/config.go`. Pane colors are optional — if omitted, they're auto-assigned from the Catppuccin Mocha palette.

## Issue Tracking

File bugs and feature requests on [GitHub Issues](https://github.com/weill-labs/amux/issues).
