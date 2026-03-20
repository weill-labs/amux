# CLAUDE.md

## Design Philosophy

See [README.md -- Philosophy](README.md#philosophy) for the project thesis and three tenets.

**Client-server architecture.** The server is a background daemon that owns PTYs and layout state. Clients connect over a Unix socket, receive layout snapshots and raw pane output, and render locally. This enables hot-reload: rebuilding the binary auto-restarts the client with new rendering code while preserving running shells.

**Pane metadata is in-memory.** All pane state lives in `mux.PaneMeta` structs on the server. No external database or state files.

**Names over IDs.** Users reference panes by name (`pane-3`) or numeric ID (`3`). `Window.ResolvePane()` handles resolution. Prefix matches are also supported.

## Architecture

### Key Abstractions

**Client-server protocol** -- Clients send `MsgTypeInput`, `MsgTypeResize`, `MsgTypeCommand`. Server sends `MsgTypePaneOutput` (raw PTY bytes per pane), `MsgTypeLayout` (layout tree snapshot), `MsgTypeRender` (legacy pre-rendered ANSI).

**`mux.Window`** -- Owns the layout tree (`LayoutCell`) and active pane. All layout operations (split, close, resize, focus) go through Window methods.

**`mux.LayoutCell`** -- Binary tree of splits. Leaves hold panes. Internal nodes hold a split direction and children. `Walk()` for traversal, `FindPane()` for lookup, `FixOffsets()` after structural changes.

**`Window.ResolvePane(ref)`** -- Accepts pane name (`pane-1`), numeric ID (`1`), or prefix match. All CLI commands route through this.

**`render.RenderFull()`** -- Composites pane content, borders with junction characters, per-pane status lines, and the global session bar into a single ANSI output string.

### Patterns To Follow

**One package per concern.** Layout logic in `mux/`, rendering in `render/`, server protocol in `server/`. Packages depend on interfaces and shared types (`proto/`), not on each other's internals.

**Unit tests for layout/rendering logic.** See `layout_test.go`, `window_test.go`, `emulator_test.go`. Use `fakePaneID()` helper to create minimal panes for testing.

**Integration tests for end-to-end behavior.** The harness in `test/server_harness_test.go` drives amux directly over the Unix socket -- no tmux dependency. Tests run in ~6s total.

**Guard against impossible states.** Minimize checks that at least one pane stays non-minimized. Restore caps height at available space. Focus fallback finds nearest pane when strict overlap matching fails.

**Save/restore cursor state in copy mode motions.** Compound motions (word, paragraph, etc.) call `moveDown()`/`moveUp()` in scanning loops. These helpers mutate `cy`/`oy` on each call, so the caller must save both values before the loop and restore them when returning `ActionNone`. Otherwise the cursor drifts silently on failed motions.

**Minimize requires a horizontal split.** `Minimize` only works on panes in a horizontal split (`splitH` / top-bottom layout). Panes in a vertical split (`splitV` / left-right layout) cannot be minimized -- the command returns an error. Tests that exercise minimize must use `splitH()`, not `splitV()`.

**Colors live in `config/config.go`.** The Catppuccin Mocha palette (`CatppuccinMocha`), letter abbreviations (`CatppuccinLetters`), and named hex constants (`DimColorHex`, `TextColorHex`) are defined once in the config package. Reference these constants instead of hardcoding hex values like `"f5e0dc"` or `"6c7086"`.

## Development

### Build And Test

```bash
make setup                         # activate repo git hooks
make build                         # build + install atomically (client hot-reloads automatically)
make test                          # run all tests
make coverage                      # merged unit + integration coverage (use this, not go test -coverprofile)
```

### Testing Live

See [README.md -- CLI Reference](README.md#cli-reference) for the full command reference. Key commands for testing:

```bash
amux                              # start or reattach
amux capture --format json        # structured JSON for agents
amux capture --format json pane-1 # single pane JSON
```

### TDD Workflow

All development follows red-green-refactor with **separate commits** for each phase:

1. **Red** -- Write failing tests. Commit them alone. Confirm they fail for the right reason (missing feature, not a syntax error).
2. **Green** -- Minimal production code to make tests pass. Commit separately.
3. **Refactor** -- Simplify, extract helpers, remove duplication. Commit separately.

The integration test harness makes this fast (~6s for the full suite).

### Test Philosophy

Tests should read like specs. Minimize logic in assertions so a human can read the test and immediately understand what behavior is expected. Prefer golden file comparisons (`assertGolden`) over inline predicate functions -- the golden file is the spec, viewable as a standalone document. Use table-driven tests for unit tests with multiple cases -- define a `tests` slice of structs, iterate with `t.Run(tt.name, ...)`, and call `t.Parallel()` in each subtest.

**Golden files** live in `test/testdata/`. Two types:

- `.golden` -- structural layout frame (status lines, borders, global bar). Open one and you see the expected screen layout.
- `.color` -- border color map using Catppuccin color initials (`R`=Rosewater, `F`=Flamingo, `M`=Mauve, `.`=dim, `|`=global bar). Shows which borders should be colored at a glance.

Regenerate goldens after intentional rendering changes: `cd test && go test -run TestGolden -update`

### Pre-Push Rebase

Rebase onto `origin/main` before the first push (`git fetch origin main && git rebase origin/main`). Multiple features often land in parallel; rebasing before push avoids repeated merge conflict resolution after the PR is open.

### Specs And Plans On Feature Branches

Commit design specs and implementation plans to the feature branch, not main. Committing to main before creating the feature branch causes divergent branches on subsequent pulls.

### Review Before Done

After creating or updating a PR, run a review pass and a simplification pass before considering the work done. Claude Code gets hook reminders for this. Codex users should use the repo PR workflow skill or perform the steps explicitly.

### Merge Conflict Resolution

After resolving merge conflicts, run `go vet ./...` locally before committing. Git auto-merge can silently produce duplicate declarations (e.g., methods defined in both sides) that compile but fail vet.

### Merge Policy

GitHub PRs for this repo are squash-only. `gh pr merge --merge` and `gh pr merge --rebase` will fail.

After merging, verify local state explicitly: check that the checkout is on `main`, the worktree is clean, and `HEAD` matches `origin/main`. If you need another change after the merge, start a fresh branch and PR instead of committing follow-up fixes on local `main`.

### Include Baseline Numbers In Performance PRs

When creating PRs that add or modify benchmarks, include a `Baseline numbers` section in the PR description with representative results in a markdown table. Include the hardware (for example, `Apple M4, macOS`) for context. Development run results are ephemeral -- the PR description is the permanent record.

### Adding A New Feature

See [CONTRIBUTING.md](CONTRIBUTING.md) for the workflow. Additional note for agents:

1. **Check what dependencies already provide.** Before designing a custom solution, check if the underlying library (for example, `charmbracelet/x/vt`) already supports the capability. Read tests and exported methods in `go/pkg/mod/`.

### Fixing A Bug

See [CONTRIBUTING.md](CONTRIBUTING.md) for the workflow.

### Adding A New CLI Command

See [CONTRIBUTING.md](CONTRIBUTING.md) for the workflow.

### Debugging Rendering

Use `amux capture --format json` to inspect composited output programmatically. To isolate whether a rendering bug is in the compositor (multi-pane compositing) or per-pane terminal emulation:

1. Capture the full session: `amux capture --format json` -- identify which panes show artifacts
2. Zoom the affected pane: `amux zoom pane-N` -- wait a few seconds, capture again
3. If the zoomed view is clean but the unzoomed view is corrupted, the bug is in the compositor or diff renderer (cell-grid boundary calculation, status bar overlay, or border compositing)
4. If the zoomed view is also corrupted, the bug is in the terminal emulator or PTY output

Trigger patterns for compositor bugs: long or truncated lines near pane boundaries, status bar overlays adjacent to wrapped content, and high-frequency output (for example, `htop` or progress bars).

### Hot-Reload

Both client and server watch the binary and re-exec on changes (`reload.go`). Running `make build` replaces the installed binary atomically, which then triggers automatic reload of both -- panes and shells are preserved across server reloads via checkpoint and restore.

`make build` also writes install metadata and refuses to overwrite the shared `~/.local/bin/amux` when that metadata shows it was last installed from a different checkout. Use `AMUX_INSTALL_FORCE=1 make build` only when you intentionally want to replace the shared binary.

Socket location: `/tmp/amux-$UID/<session-name>`

## Configuration

See [README.md -- Configuration](README.md#configuration) for the `hosts.toml` format. Config parsing lives in `config/config.go`. Pane colors are optional -- if omitted, they're auto-assigned from the Catppuccin Mocha palette.

## Issue Tracking

File bugs and feature requests for this repository in the [`amux` project](https://linear.app/weill-labs/project/amux-b3a52334f77c). GitHub Issues is not actively monitored.
