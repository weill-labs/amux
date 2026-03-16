# Benchmark Suite Design

## Motivation

amux has zero performance benchmarks today. We need to:
1. Measure amux vs tmux performance across key dimensions
2. Detect regressions in CI on every PR
3. Track performance trends over time via GitHub Pages

## Approach

All-Go benchmark suite using `testing.B`. Microbenchmarks test isolated components; integration benchmarks use the existing test harnesses (`ServerHarness`, `AmuxHarness`) and a new lightweight `TmuxBenchHarness` for tmux comparison. All output is `benchstat`-compatible. Everything runs in CI.

## Dimensions

| Dimension | What it measures |
|---|---|
| Agent-workflow latency | Time for an agent to issue a CLI command and get structured output |
| Rendering throughput | How fast the compositor renders high-bandwidth output |
| Input-to-screen latency | Keystroke to visible character on screen |
| Multi-pane scaling | Performance degradation as pane count increases |
| Hot-reload overhead | Time from binary rebuild to client reconnect (amux-only) |

## File Organization

```
internal/
  render/
    compositor_bench_test.go    # Rendering throughput microbenchmarks
  server/
    protocol_bench_test.go      # Protocol encode/decode microbenchmarks
  mux/
    layout_bench_test.go        # Layout operations (split, walk, resize)
    emulator_bench_test.go      # VT emulator write/render throughput
test/
    bench_test.go               # Integration benchmarks (amux + tmux comparison)
```

## Microbenchmarks (CI-safe)

All use Go's `testing.B` for `benchstat`-compatible output.

### Rendering throughput (`compositor_bench_test.go`)

- `BenchmarkRenderFull/panes_1` / `panes_4` / `panes_10` / `panes_20` — build a layout tree with N panes, populate emulators with realistic terminal content (shell prompt + `ls` output), call `RenderFull()` in a loop. Measures compositor cost as pane count grows.
- `BenchmarkClipLine` — the hot path in `blitPane()` that clips ANSI-escaped lines. Feed it realistic lines with escape sequences.
- `BenchmarkBuildBorderMap` — border junction calculation, scales with layout complexity.

### Protocol encode/decode (`protocol_bench_test.go`)

- `BenchmarkWriteMsg_PaneOutput/bytes_256` / `bytes_4096` / `bytes_32768` — encode a `MsgTypePaneOutput` with realistic payload sizes. Measures gob overhead.
- `BenchmarkReadMsg_PaneOutput/bytes_256` / `bytes_4096` / `bytes_32768` — decode the same.
- `BenchmarkWriteMsg_Layout/panes_1` / `panes_10` / `panes_20` — encode a `MsgTypeLayout` with N panes.

### Layout operations (`layout_bench_test.go`)

- `BenchmarkSplit` / `BenchmarkClose` / `BenchmarkWalk` / `BenchmarkFixOffsets` — layout tree manipulation at various depths.
- `BenchmarkResolvePane/panes_1` / `panes_10` / `panes_20` — name/ID/prefix resolution scaling.

### Emulator throughput (`emulator_bench_test.go`)

- `BenchmarkEmulatorWrite` — feed N bytes of realistic terminal output (mixed text + ANSI escapes) into the VT emulator.
- `BenchmarkEmulatorRender` — call `Render()` after writing content. Measures screen extraction cost.

## Integration Benchmarks

Live in `test/bench_test.go`. Use existing harnesses for amux, a new lightweight `TmuxBenchHarness` for tmux comparison.

### TmuxBenchHarness

Minimal wrapper for tmux comparison benchmarks. Three methods:
- Start a tmux session with a given geometry
- Run tmux commands (`capture-pane -p`, `send-keys`, `list-panes`)
- Cleanup (kill session)

No synchronization primitives, no split helpers — just raw tmux commands timed in `testing.B` loops.

### Agent-workflow latency

Amux uses `ServerHarness.runCmd()` (synchronous Unix socket round-trip). Tmux uses `exec.Command("tmux", ...)`.

- `BenchmarkCapture/amux` / `tmux` — full capture round-trip.
- `BenchmarkList/amux` / `tmux` — pane listing. Amux: `amux list`. Tmux: `tmux list-panes -F "#{pane_id} #{pane_title}"`.
- `BenchmarkSendKeys/amux` / `tmux` — inject keystrokes via CLI.
- Each runs with sub-benchmarks for 1 pane and 4 panes.

### Input-to-screen latency

Uses `AmuxHarness` for amux (full client+server stack). `TmuxBenchHarness` for tmux.

- `BenchmarkInputLatency/amux` / `tmux` — send a unique marker string, then use `wait-for` (amux, blocking) or poll `capture-pane` (tmux) until it appears. Measure wall-clock time.
- Unique token per iteration (`BENCH-0001`, `BENCH-0002`, ...) to avoid false positives.
- For tmux, poll at 5ms intervals since tmux has no blocking wait-for equivalent.

### Rendering throughput under load

- `BenchmarkThroughput/amux` / `tmux` — send `seq 1 10000` to a pane, then `echo DONE`. Measure wall-clock time from send until `DONE` appears.
- Variant with 4 panes running simultaneously.

### Multi-pane scaling

- `BenchmarkSplitScale/amux/panes_N` / `tmux/panes_N` — time creating N panes (1, 4, 10, 20). Amux: `runCmd("split")`. Tmux: `tmux split-window`.
- `BenchmarkCaptureScale/amux/panes_N` / `tmux/panes_N` — capture latency with 1, 4, 10, 20 panes.

### Hot-reload (amux-only)

- `BenchmarkHotReload` — rebuild the binary via `go build`, measure time until the client reconnects and re-renders. Uses `AmuxHarness`.
- Reports total time and reconnect-only time (excluding build) via `b.ReportMetric()`.

## Implementation Details

### Test isolation

Each benchmark function gets its own harness instance (own session, own server). Harness `cleanup()` handles teardown. Benchmarks use `b.StopTimer()` during setup/teardown.

### Realistic content

Emulator and compositor benchmarks use realistic terminal content: shell prompts, `ls` output, ANSI color sequences. Not random bytes — real terminal workloads include escape sequences that exercise the parser differently than raw text.

### Statistical rigor

Start pragmatic: `-count=5` for microbenchmarks, `-count=7` for integration benchmarks (more variance). Use `benchstat`-compatible output format from day one so upgrading rigor is just "run more iterations."

## CI Integration

### `benchmark.yml` — runs on every push to `main` and every PR

```yaml
# Pseudocode
- apt-get install -y tmux
- go test -bench=. -benchmem -count=5 ./internal/... > bench-micro.txt
- go test -bench=. -benchmem -count=7 ./test/ -timeout 300s >> bench-micro.txt

# On main: update trend data
- gobenchdata merge bench-micro.txt → gh-pages JSON
- commit to gh-pages

# On PR: compare against main baseline
- download main benchmark artifact
- benchstat main.txt pr.txt > comparison.txt
- post comparison as PR comment
- fail check if regression >15% with p < 0.05
```

### Regression threshold

15% with p < 0.05 (via `benchstat`). CI runners have noisy neighbors; the 15% floor avoids false positives. Can tighten later as baseline variance is understood.

### Trend visualization

`gobenchdata` generates a trend chart on GitHub Pages at `https://weill-labs.github.io/amux/`. Shows ns/op, B/op, and allocs/op over time for each microbenchmark.

### Local workflow

```bash
# Before change
go test -bench=. -benchmem -count=5 ./... > old.txt

# After change
go test -bench=. -benchmem -count=5 ./... > new.txt

# Compare
benchstat old.txt new.txt
```

## Decision Log

| Decision | Rationale |
|---|---|
| All-Go (`testing.B`) over shell scripts | Unified measurement, `benchstat` compatibility, reuses existing harnesses |
| Everything in CI (including integration) | Ubuntu runners have tmux; `benchstat` handles variance statistically |
| `gobenchdata` + GitHub Pages | Free, automated, visual trends, no infrastructure to maintain |
| 15% regression threshold | Balances CI noise vs catching real regressions; tunable later |
| Lightweight `TmuxBenchHarness` | Minimal code for comparison; not a full test harness |
| `-count=7` for integration benchmarks | More samples to compensate for higher wall-clock variance |
