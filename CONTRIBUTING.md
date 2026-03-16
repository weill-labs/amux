# Contributing to amux

## Prerequisites

- Go 1.25+
- tmux (required for integration tests)

## Build

```bash
go build -o ~/.local/bin/amux .    # build + install
go test ./...                       # run all tests
```

Hot-reload: both client and server watch the binary and re-exec on changes. Running `go build` triggers automatic reload — panes and shells are preserved.

To test manually after building:

```bash
amux                              # start or reattach to a session
amux capture --format json        # structured JSON output for agents
```

## Test

amux uses test-driven development. Write a failing test first, then implement.

### Integration tests

The harness in `test/harness_test.go` runs amux inside a real tmux session, sends keys via `tmux send-keys`, and asserts on screen content via `tmux capture-pane`. The full suite runs in ~6s.

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
- Include tests for new behavior
- Bug fixes include a regression test
- Keep PRs focused — don't mix features with refactors

Check [GitHub Issues](https://github.com/weill-labs/amux/issues) for open tasks and to report bugs.
