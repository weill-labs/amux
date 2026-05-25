# Client renderer benchmarks

`BenchmarkClientRendererBusyMultiPaneRenderLoop` is a reproducible local
workload for the May 25, 2026 busy client profiles. It uses a 240x82 client
with ten visible panes arranged as a 2x5 grid, sends six short styled ANSI
bursts to each of four panes, publishes pane capture snapshots, and runs one
dirty diff render for each benchmark operation.

Run the CI-safe benchmark with allocation reporting:

```bash
go test ./internal/client -run '^$' -bench '^BenchmarkClientRendererBusyMultiPaneRenderLoop$' -benchmem -count=10 -timeout 120s
```

Capture a CPU profile:

```bash
go test ./internal/client -run '^$' -bench '^BenchmarkClientRendererBusyMultiPaneRenderLoop$' -benchtime=10s -cpuprofile /tmp/lab1909-client-renderer.cpu.pprof -benchmem -timeout 120s
go tool pprof -top -cum -nodecount=30 /tmp/lab1909-client-renderer.cpu.pprof
```

Capture an allocation profile:

```bash
go test ./internal/client -run '^$' -bench '^BenchmarkClientRendererBusyMultiPaneRenderLoop$' -benchtime=10s -memprofile /tmp/lab1909-client-renderer.alloc.pprof -memprofilerate=1 -benchmem -timeout 120s
go tool pprof -top -alloc_space -nodecount=30 /tmp/lab1909-client-renderer.alloc.pprof
```

The benchmark is intentionally a combined harness rather than a replacement for
the narrower capture and compositor microbenchmarks. Use it for follow-up work
that needs to see interactions among `HandlePaneOutputInfo`,
`publishPaneCapture`, `capturePaneRenderSnapshot`, and
`RenderDiffWithOverlayDirtyStats` under a live-shaped busy pane mix.
