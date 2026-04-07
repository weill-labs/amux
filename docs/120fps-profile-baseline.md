# 120fps Profile Baseline

## Scope

- Date: 2026-04-04
- Commit: `54b081b4bc612da4dcc5b895be0b7fd1cda43327` (`54b081b`)
- Binary: `amux` build `54b081b`
- Host: Apple M4, macOS 15.6
- Session: `profile-baseline-744`
- Window size: `200x60`
- Layout: 10 panes created with `spawn` and rebalanced with `equalize --all`
- Sustained output panes: `2`, `3`, `4`
- Load generator: `while true; do seq 1 1000; done`

This baseline used the built-in pprof endpoint enabled via `[debug].pprof = true`.
That endpoint is wired up in the server bootstrap path, so the profiles below are
server-side profiles under sustained pane output load, not end-to-end client render
profiles.

Two consequences follow from that scope:

1. `github.com/weill-labs/amux/internal/mux.(*vtEmulator).Write` is heavily sampled.
2. `buildGridWithOverlayDirty`, `DiffGrid`, and `emitDiffWithProfile` are not sampled
   at all in this profile even though their symbols are present in the binary, because
   the diff renderer is invoked from the client path in `internal/client`.

## Commands

```bash
make install

AMUX_CONFIG=.tmp/120fps-profile-baseline/config.toml amux new profile-baseline-744
AMUX_CONFIG=.tmp/120fps-profile-baseline/config.toml amux -s profile-baseline-744 resize-window 200 60
for i in $(seq 2 10); do
  AMUX_CONFIG=.tmp/120fps-profile-baseline/config.toml amux -s profile-baseline-744 spawn --name load-$i
done
AMUX_CONFIG=.tmp/120fps-profile-baseline/config.toml amux -s profile-baseline-744 equalize --all

AMUX_CONFIG=.tmp/120fps-profile-baseline/config.toml amux -s profile-baseline-744 send-keys 2 'while true; do seq 1 1000; done' Enter
AMUX_CONFIG=.tmp/120fps-profile-baseline/config.toml amux -s profile-baseline-744 send-keys 3 'while true; do seq 1 1000; done' Enter
AMUX_CONFIG=.tmp/120fps-profile-baseline/config.toml amux -s profile-baseline-744 send-keys 4 'while true; do seq 1 1000; done' Enter

AMUX_CONFIG=.tmp/120fps-profile-baseline/config.toml amux -s profile-baseline-744 debug profile --duration 30s > .tmp/120fps-profile-baseline/cpu.pprof
curl --unix-socket "$(AMUX_CONFIG=.tmp/120fps-profile-baseline/config.toml amux -s profile-baseline-744 debug socket)" \
  http://amux/debug/pprof/heap?gc=1 > .tmp/120fps-profile-baseline/heap.pprof
```

## CPU Profile

Profile summary:

- Duration: `30.21s`
- Total sampled CPU: `13.52s`
- Sample ratio: `44.76%`

Top 10 functions by cumulative CPU:

| Rank | Function | Flat | Cum | Cum % |
|------|----------|------|-----|-------|
| 1 | `github.com/weill-labs/amux/internal/mux.(*Pane).actorLoop` | `0.00s` | `10.05s` | `74.33%` |
| 2 | `github.com/charmbracelet/x/ansi.(*Parser).Advance` | `0.00s` | `10.02s` | `74.11%` |
| 3 | `github.com/charmbracelet/x/ansi.(*Parser).advance` | `0.01s` | `10.02s` | `74.11%` |
| 4 | `github.com/charmbracelet/x/vt.(*Emulator).Write` | `0.00s` | `10.02s` | `74.11%` |
| 5 | `github.com/charmbracelet/x/vt.(*Emulator).parseBytes` | `0.00s` | `10.02s` | `74.11%` |
| 6 | `github.com/weill-labs/amux/internal/mux.(*Pane).readLoop.(*Pane).applyOutput.func1` | `0.00s` | `10.02s` | `74.11%` |
| 7 | `github.com/weill-labs/amux/internal/mux.(*vtEmulator).Write` | `0.00s` | `10.02s` | `74.11%` |
| 8 | `github.com/weill-labs/amux/internal/mux.paneActorValue[go.shape.uint64].func2` | `0.00s` | `10.02s` | `74.11%` |
| 9 | `github.com/charmbracelet/x/ansi.(*Parser).performAction` | `0.00s` | `10.01s` | `74.04%` |
| 10 | `github.com/charmbracelet/x/vt.(*Emulator).handleControl` | `0.00s` | `9.96s` | `73.67%` |

Actual time in the requested functions:

| Function | Flat | Cum | Result |
|----------|------|-----|--------|
| `github.com/weill-labs/amux/internal/mux.(*vtEmulator).Write` | `0.00s` | `10.02s` | Sampled heavily in the server profile |
| `github.com/weill-labs/amux/internal/render.(*Compositor).buildGridWithOverlayDirty` | `not sampled` | `not sampled` | `go tool pprof -list` returned `no matches found for regexp` |
| `github.com/weill-labs/amux/internal/render.DiffGrid` | `not sampled` | `not sampled` | `go tool pprof -list` returned `no matches found for regexp` |
| `github.com/weill-labs/amux/internal/render.emitDiffWithProfile` | `not sampled` | `not sampled` | `go tool pprof -list` returned `no matches found for regexp` |

Interpretation:

- Under this workload, the server spends its time ingesting pane output and growing
  retained scrollback, not diff-rendering the client frame.
- This is consistent with the current architecture: `ClientRenderer.RenderDiff()`
  calls into `Renderer.RenderDiffWithOverlayDirty()`, so the diff renderer runs on
  the client side while the pprof endpoint profiles the server.

CPU graph:

- [CPU SVG](pprof/120fps-profile-baseline-cpu.svg)

## Heap Profile

Heap capture notes:

- `amux debug heap` is a text summary endpoint (`/debug/pprof/heap?debug=1`).
- The raw heap profile used for `go tool pprof` was fetched directly from the
  session pprof socket at `/debug/pprof/heap?gc=1`.

In-use heap summary:

- Total in-use heap: `79.24MB`
- Largest in-use sites:
  - `github.com/charmbracelet/x/ansi.(*Parser).SetDataSize`: `40.01MB`
  - `github.com/charmbracelet/ultraviolet.NewBuffer`: `20.47MB`
  - `slices.Clone[...]`: `12.50MB`

Top 10 allocation sites by `alloc_space`:

| Rank | Function | Flat alloc | Flat % | Cum alloc |
|------|----------|------------|--------|-----------|
| 1 | `github.com/weill-labs/amux/internal/mux.(*Pane).recordScrollbackPush` | `71560.60MB` | `99.27%` | `71560.60MB` |
| 2 | `slices.Clone[go.shape.[]github.com/charmbracelet/ultraviolet.Cell,...]` | `300.10MB` | `0.42%` | `300.10MB` |
| 3 | `github.com/charmbracelet/x/ansi.(*Parser).SetDataSize` | `40.55MB` | `0.056%` | `40.55MB` |
| 4 | `github.com/weill-labs/amux/internal/capture.TerminalFromState` | `22.10MB` | `0.031%` | `40.60MB` |
| 5 | `io.copyBuffer` | `21.66MB` | `0.030%` | `23.16MB` |
| 6 | `github.com/weill-labs/amux/internal/mux.(*vtEmulator).TerminalState` | `21.60MB` | `0.030%` | `21.60MB` |
| 7 | `github.com/charmbracelet/ultraviolet.NewBuffer` | `20.98MB` | `0.029%` | `20.98MB` |
| 8 | `fmt.Sprintf` | `17.00MB` | `0.024%` | `18.50MB` |
| 9 | `bytes.growSlice` | `14.39MB` | `0.020%` | `14.39MB` |
| 10 | `github.com/charmbracelet/x/vt.(*Emulator).handlePrint` | `11.00MB` | `0.015%` | `11.00MB` |

Heap graphs:

- [Alloc Space SVG](pprof/120fps-profile-baseline-alloc-space.svg)
- [In-Use Space SVG](pprof/120fps-profile-baseline-inuse-space.svg)

## Baseline Takeaways

- The current built-in pprof path is enough to baseline server-side pane ingestion,
  VT parsing, and scrollback growth under load.
- It is not enough to baseline the client diff renderer. The requested render-path
  functions show `0s` sampled time here because the server pprof endpoint never sees
  the client render loop.
- The strongest server-side signal in both CPU and allocation reports is retained
  scrollback churn:
  - `(*vtEmulator).Write` sits on the hot CPU path at `10.02s cumulative`.
  - `(*Pane).recordScrollbackPush` accounts for `71.56GB` of `alloc_space`.

## Follow-Up

To get a real render-pipeline baseline for `buildGridWithOverlayDirty`,
`DiffGrid`, and `emitDiffWithProfile`, use the client-side profiling path added
after this baseline:

```bash
amux debug client-profile --duration 30s > client-cpu.pprof.gz
amux debug client-heap > client-heap.txt
```

The server pprof endpoint still cannot answer client render-loop questions on
its own.
