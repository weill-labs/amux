# Contributing to amux

## Prerequisites

- Go 1.25+
- tmux (optional, only needed for comparison benchmarks)

## Install

```bash
make setup                         # activate repo git hooks
make install                       # install amux
go test ./...                       # run all tests
```

Hot-reload: both client and server watch the binary and re-exec on changes. Use `make install` so the installed binary is replaced atomically before reload, preserving panes and shells.

To test manually after building:

```bash
amux                              # start or reattach to a session
amux capture --format json        # structured JSON output for agents
```

## AI Agents

- Shared repo instructions live in `AGENTS.md`.
- `CLAUDE.md` is a thin Claude Code shim plus notes about Claude-only hooks.
- Claude Code also loads `.claude/settings.json` and `.claude/hooks/`.
- Codex reads `AGENTS.md` and can discover repo skills from `.agents/skills/`.
- When starting a Linear issue in an amux pane, prefer `scripts/set-pane-issue.sh LAB-XXX` so the current pane gets tagged automatically.
- After `gh pr create`, run `scripts/watch-pr-ci.sh` once for that PR. For later updates to an open PR, prefer `scripts/push-and-watch-ci.sh` over bare `git push`.
- If `scripts/watch-pr-ci.sh` reports failures, inspect the printed failed logs, fix the issue or explain why it is flaky/unrelated before handing the PR back.
- When leading a worker session, `scripts/check-worker-ci.sh` reports failing PRs, maps them back to owning panes, and nudges idle workers.
- `scripts/check-pr-ready.sh` finds worker PRs that are green, Claude-approved, and conflict-free, then nudges the owning pane that the PR is ready for human merge.
- After pushing fixes for Claude review findings, `scripts/check-claude-review.sh` reports whether the latest Claude verdict is `lgtm` or `findings`; add `--watch` to wait for the next Claude review comment.
- Run `make setup` after cloning so repo Git hooks are active regardless of which tool you use.
- When opening a PR from an amux pane, prefer `scripts/gh-pr-create.sh ...` so pane PR metadata syncs for any agent; later `git push` calls re-sync via the repo `pre-push` hook.
- Optional for Codex users: trust the repo, then install the OpenAI Docs MCP server with `codex mcp add openaiDeveloperDocs --url https://developers.openai.com/mcp`.

## Test

amux uses test-driven development with separate commits per phase. See [AGENTS.md — TDD Workflow](AGENTS.md#tdd-workflow) for the full red-green-refactor commit structure.

### Integration tests

The harness in `test/server_harness_test.go` drives amux directly over the Unix socket. The full suite runs in ~6s.

Keep startup behavior per-session. Parallel tests may bring up many servers at once, so server startup must not do cross-session socket cleanup or other global sweeps that can remove another live test session's socket.

If you add or change harness startup signals, make "ready" mean the next harness operation can succeed immediately. For example, a ready signal used before client attach should only fire once the server can actually accept that attach.

```bash
go test -v -run TestYourFeature ./test/ -timeout 30s
```

### Unit tests

Use table-driven tests with `t.Run(tt.name, ...)` and `t.Parallel()`. See `layout_test.go`, `window_test.go`, `emulator_test.go` for examples.

Root CLI subprocess tests must use the shared hermetic helper in the root package tests. Do not open-code `exec.Command(os.Args[0], ...)` or inherit ambient `AMUX_SESSION` / `TMUX` state in those tests.

If a change touches CLI dispatch branches in `main.go`, add direct unit coverage for those touched lines as well (for example in `main_test.go`). Hermetic subprocess tests still cover end-to-end behavior, but Codecov patch coverage measures the changed `main.go` lines directly and may not credit coverage that only flows through the subprocess path.

### Golden files

Golden files live in `test/testdata/`:
- `.golden` — structural layout frame (status lines, borders, global bar)
- `.color` — border color map using Catppuccin color initials

Regenerate after intentional rendering changes:

```bash
cd test && go test -run TestGolden -update
```

## Adding a feature

1. Check what dependencies already provide before building a custom solution
2. Write an integration test first in `test/`
3. Implement the feature
4. Verify: `go test -v -run TestYourFeature ./test/ -timeout 30s`
5. Add unit tests for complex logic (layout algorithms, rendering, protocol)

## Fixing a bug

1. Write a failing regression test that reproduces the bug
2. Fix the bug
3. Verify: `go test ./...`

## Adding a CLI command

1. Add the command to the `switch` in `main.go` (use `runServerCommand()` for server-side commands)
2. Add the handler in `internal/server/client_conn.go` `handleCommand()` method
3. Update `printUsage()` in `main.go`
4. Write an integration test in `test/`

## Pull requests

- One logical change per PR
- Tests are required (see sections above)
- Keep PRs focused — avoid mixing features with refactors

Check the [Linear LAB project](https://linear.app/weill-labs/team/LAB) for open tasks and to report bugs.

By submitting a pull request, you agree that your contribution is licensed under the [MIT License](LICENSE).
