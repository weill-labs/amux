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

**Inject dependencies, do not add package-level `var` for test seams.** When production code needs a swappable dependency (e.g., clipboard command, time function, exec resolver), pass it as a function parameter or struct field -- never as a mutable package-level `var`. Tests pass stubs directly; production call sites pass the real implementation. This keeps tests parallel and eliminates shared mutable state. See PR #388 for the canonical pattern.

**Use the persistent harness when server lifetime matters.** Prefer `newServerHarnessPersistent()` for integration tests that must keep the server alive independent of client detach timing or transient attachment windows. Use the default harness when exit-on-unattached behavior is part of the behavior under test.

**Guard against impossible states.** Focus fallback finds nearest pane when strict overlap matching fails.

**Save/restore cursor state in copy mode motions.** Compound motions (word, paragraph, etc.) call `moveDown()`/`moveUp()` in scanning loops. These helpers mutate `cy`/`oy` on each call, so the caller must save both values before the loop and restore them when returning `ActionNone`. Otherwise the cursor drifts silently on failed motions.

**Colors live in `config/config.go`.** The Catppuccin Mocha palette (`CatppuccinMocha`), letter abbreviations (`CatppuccinLetters`), and named hex constants (`DimColorHex`, `TextColorHex`) are defined once in the config package. Reference these constants instead of hardcoding hex values like `"f5e0dc"` or `"6c7086"`.

## Development

### Build And Test

```bash
make setup                         # activate repo git hooks
make install                       # install amux (client hot-reloads automatically)
go test ./... -timeout 120s        # run all tests
make coverage                      # merged unit + integration coverage (use this, not go test -coverprofile)
```

**Reproduce CI from a clean shell when running inside `amux`/tmux.** Clipboard and harness behavior can change when `AMUX_SESSION` or `TMUX` are set. For CI-style verification from an attached pane, prefer `env -u AMUX_SESSION -u TMUX scripts/coverage.sh --ci`, and prefix other CI-style test commands the same way when you need the same environment.

### Testing Live

See [README.md -- CLI Reference](README.md#cli-reference) for the full command reference. Key commands for testing:

```bash
amux                              # start or reattach
amux capture --format json        # structured JSON for agents
amux capture --format json pane-1 # single pane JSON
```

### Working In amux

When working in an amux pane, start Linear work with the helper script so the current pane is tagged automatically. Outside an amux pane, pass the pane explicitly. After `gh pr create` or `git push`, Claude's PR hook syncs `pr=NUMBER` back onto the current pane automatically. Other agents can run the sync script manually:

```bash
scripts/set-pane-issue.sh LAB-XXX
scripts/set-pane-issue.sh pane-3 LAB-XXX
scripts/sync-pane-pr-meta.sh
scripts/sync-pane-pr-meta.sh 123
```

Claude's stop hook also checks for missing pane issue metadata before the session ends. Other agents can run `scripts/check-pane-issue-meta.sh` and `scripts/sync-pane-pr-meta.sh` manually if needed.

### TDD Workflow

All development follows red-green-refactor with **separate commits** for each phase:

1. **Red** -- Write failing tests. Commit them alone. Confirm they fail for the right reason (missing feature, not a syntax error).
2. **Green** -- Minimal production code to make tests pass. Commit separately.
3. **Refactor** -- Simplify, extract helpers, remove duplication. Commit separately.

The integration test harness makes this fast (~6s for the full suite).

### Test Philosophy

Tests should read like specs. Minimize logic in assertions so a human can read the test and immediately understand what behavior is expected. Prefer golden file comparisons (`assertGolden`) over inline predicate functions -- the golden file is the spec, viewable as a standalone document. Use table-driven tests for unit tests with multiple cases -- define a `tests` slice of structs, iterate with `t.Run(tt.name, ...)`, and call `t.Parallel()` in each subtest.

When a change adds a new test or modifies an existing test, run that targeted test slice with `-count=100` before calling the work done. Treat any failure in those repeated runs as a flake to investigate, not as an acceptable one-off.

**Root CLI subprocess tests must use the shared hermetic helper.** Do not open-code `exec.Command(os.Args[0], ...)` or inherit ambient `AMUX_SESSION` / `TMUX` state in root package tests; route those tests through the shared helper so they always run with an isolated session and scrubbed env.

**Changes to `main.go` CLI dispatch need direct unit coverage on the touched lines.** Keep the hermetic subprocess tests for end-to-end CLI behavior, but when a change touches the dispatch branches in `main.go`, add direct unit coverage (for example in `main_test.go`) for those specific lines too. Codecov patch coverage measures the changed `main.go` lines directly and may miss coverage that only arrives through subprocess tests.

**Golden files** live in `test/testdata/`. Two types:

- `.golden` -- structural layout frame (status lines, borders, global bar). Open one and you see the expected screen layout.
- `.color` -- border color map using Catppuccin color initials (`R`=Rosewater, `F`=Flamingo, `M`=Mauve, `.`=dim, `|`=global bar). Shows which borders should be colored at a glance.

Regenerate goldens after intentional rendering changes: `cd test && go test -run TestGolden -update`

### Pre-Push Rebase

Rebase onto `origin/main` before the first push (`git fetch origin main && git rebase origin/main`). Multiple features often land in parallel; rebasing before push avoids repeated merge conflict resolution after the PR is open.

Do not `git pull` a dirty local `main`. If `main` has uncommitted work, leave it alone and start the next change from a fresh branch based on `origin/main` instead. Do not use `git worktree` unless the user explicitly asks for it.

If a PR is already open and `git fetch origin main` or `git pull` advances `origin/main`, refresh that PR branch onto `origin/main` before treating it as current again. After the refresh, rerun verification on the rebased branch before pushing.

### Specs And Plans On Feature Branches

Commit design specs and implementation plans to the feature branch, not main. Committing to main before creating the feature branch causes divergent branches on subsequent pulls.

### PR Title And Description

PR title and description are the permanent record of why a change was made. Write them for a reviewer seeing the diff for the first time.

**Title**: State what changed in imperative mood, under 70 characters. Example: "Timestamp crash checkpoint filenames to prevent overwriting". Omit ticket prefixes like `LAB-314:` — link tickets in the description body instead.

**Description** must include four sections:

1. **Motivation** -- Why this change? What broke, what was missing, or what user need does it address? One to three sentences.
2. **Summary** -- What changed? Bullet the key changes. Describe the PR as a complete unit, not per-commit.
3. **Testing** -- How was it verified? Include the exact test commands a reviewer can copy-paste.
4. **Review focus** -- What should reviewers look at? Call out non-obvious design decisions, edge cases, or areas where you are least confident.

Use matter-of-fact language. State what the PR does, not how good it is. Avoid vague qualifiers like "robust", "comprehensive", "elegant", or "production-ready". If a Linear issue exists, add `Closes LAB-NNN` at the bottom.

### Review Before Done

After creating or updating a PR, run a review pass and a simplification pass before considering the work done. Claude Code gets hook reminders for this. Codex users should use the repo PR workflow skill or perform the steps explicitly.

Prefer external review tooling like `codex review` when it returns promptly, but do not block the PR on it. If the tool stalls or is unavailable, do a manual diff review and say that explicitly.

If a change in this repo is ready for review, open the PR proactively instead of asking whether to make one.

### User Handoffs

Before stopping to wait for user input, suggest the next concrete action the user should take or approve. Do not end at "waiting on you" without a specific next step.

If you ran `$postmortem`, provide the log path, summarize the key learnings, list the concrete action items, and say whether you already implemented them or left them for follow-up.

### Merge Conflict Resolution

After resolving merge conflicts, run `go vet ./...` locally before committing. Git auto-merge can silently produce duplicate declarations (e.g., methods defined in both sides) that compile but fail vet.

### Verify Mergeability Before Declaring PRs Ready

Before telling the user a PR is safe to merge, check for merge conflicts with main: `git fetch origin main && git merge-tree --write-tree origin/main origin/BRANCH`. If there are conflicts, rebase the branch onto `origin/main` and resolve them before declaring ready.

### Merge Policy

GitHub PRs for this repo are squash-only. `gh pr merge --merge` and `gh pr merge --rebase` will fail.

After merging, verify local state explicitly: check that the checkout is on `main`, the worktree is clean, and `HEAD` matches `origin/main`. If you need another change after the merge, start a fresh branch and PR instead of committing follow-up fixes on local `main`.

Claude's post-merge hook now auto-runs `scripts/post-merge-main-sync.sh` after a successful `gh pr merge`, which checks out `main` and runs `git pull --ff-only`. Other agents can run that script manually when they need the same behavior.

After merging, explicitly run `$postmortem`. A short manual recap is not a substitute for the postmortem workflow, and do not claim it ran unless you have the logged `~/.local/share/postmortems/...` path.

In the final merge closeout, tell the user what the postmortem found and what follow-up actions, if any, came out of it, alongside the logged path.

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

For cursor bugs in TUIs that may draw their own cursor (for example, Claude Code), compare `amux capture --format json` with `amux capture --ansi pane-N`. If ANSI shows a reverse-video block in pane content but JSON reports a stale cursor position, treat the app-drawn cursor as the visible source of truth and debug cursor-block detection separately from emulator cursor metadata.

Trigger patterns for compositor bugs: long or truncated lines near pane boundaries, status bar overlays adjacent to wrapped content, and high-frequency output (for example, `htop` or progress bars).

### Hot-Reload

Both client and server watch the binary and re-exec on changes (`reload.go`). Running `make install` replaces the installed binary atomically, which then triggers automatic reload of both -- panes and shells are preserved across server reloads via checkpoint and restore.

Socket location: `/tmp/amux-$UID/<session-name>`

## Configuration

See [README.md -- Configuration](README.md#configuration) for the `hosts.toml` format. Config parsing lives in `config/config.go`. Pane colors are optional -- if omitted, they're auto-assigned from the Catppuccin Mocha palette.

## Issue Tracking

File bugs and feature requests for this repository in the [`amux` project](https://linear.app/weill-labs/project/amux-b3a52334f77c). GitHub Issues is not actively monitored.
