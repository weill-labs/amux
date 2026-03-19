# Contributing to amux

## Prerequisites

- Go 1.25+
- tmux (optional, only needed for comparison benchmarks)

## Build

```bash
make setup                         # activate repo git hooks
make build                         # build + install atomically
go test ./...                       # run all tests
```

Hot-reload: both client and server watch the binary and re-exec on changes. Use `make build` so the installed binary is replaced atomically before reload — panes and shells are preserved.

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
- Run `make setup` after cloning so repo Git hooks are active regardless of which tool you use.
- Optional for Codex users: trust the repo, then install the OpenAI Docs MCP server with `codex mcp add openaiDeveloperDocs --url https://developers.openai.com/mcp`.

## Test

amux uses test-driven development with separate commits per phase. See [AGENTS.md — TDD Workflow](AGENTS.md#tdd-workflow) for the full red-green-refactor commit structure.

### Integration tests

The harness in `test/server_harness_test.go` drives amux directly over the Unix socket. The full suite runs in ~6s.

```bash
go test -v -run TestYourFeature ./test/ -timeout 30s
```

### Unit tests

Use table-driven tests with `t.Run(tt.name, ...)` and `t.Parallel()`. See `layout_test.go`, `window_test.go`, `emulator_test.go` for examples.

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
