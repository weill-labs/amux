# 120fps Benchmark Baseline

Collected on 2026-04-04 from five benchmark samples per row unless noted otherwise.

## Hardware

- CPU/OS: `Darwin Charless-Mac-mini.local 24.6.0 Darwin Kernel Version 24.6.0: Mon Jul 14 11:30:40 PDT 2025; root:xnu-11417.140.69~1/RELEASE_ARM64_T8132 arm64`
- Go: `go version go1.25.6 darwin/arm64`
- Frame budget target: `8.33ms` (`8,330,000 ns/op`) for 120fps

## Commands

- `env -u AMUX_SESSION -u TMUX go test ./internal/render/ -bench . -benchmem -count=5`
- `env -u AMUX_SESSION -u TMUX go test ./internal/client/ -bench . -benchmem -count=5`
- `env -u AMUX_SESSION -u TMUX go test ./internal/mux/ -bench . -benchmem -count=5`
- `env -u AMUX_SESSION -u TMUX go test ./internal/server/ -bench . -benchmem -count=5`
- `env -u AMUX_SESSION -u TMUX go test ./internal/proto/ -bench . -benchmem -count=5`
- `env -u AMUX_SESSION -u TMUX go test ./test/ -bench . -benchmem -count=5`
- `env -u AMUX_SESSION -u TMUX go test ./test/ -run '^$' -bench . -benchmem -count=5`
- `cd ~/sync/github/ultraviolet/ultraviolet && env -u AMUX_SESSION -u TMUX go test ./... -bench . -benchmem -count=5`

## Notes

- LAB-745 listed `render`, `client`, `mux`, `test`, and `ultraviolet`. This baseline also includes `internal/server` and `internal/proto` so the document covers every benchmark currently checked into amux.
- The exact `./test` command from LAB-745 failed before running benchmarks because `TestSendKeysEncodeParityMatrix` is currently red. The end-to-end numbers below come from the benchmark-only rerun with `-run '^$'`.
- Observed failing parity cases during the exact `./test` run: `end_key`, `function_key_f11`, `function_key_f12`, `home_key`, `keypad_digit_2`, `keypad_digit_3`, `keypad_enter`, `keypad_equal`, `meta_printable`, `meta_shifted_printable`.
- Raw outputs: `/tmp/amux-120fps-baseline-20260404-031331`

## Summary

- Benchmarks recorded: `135` median rows across amux and ultraviolet.
- Over budget: `29` rows exceeded `8.33ms`.
- Slowest median rows:
  - `amux/test (benchmark-only rerun)` / `test` / `BenchmarkHotReload-10`: `1,653,608,250 ns/op`
  - `amux/test (benchmark-only rerun)` / `test` / `BenchmarkSplitScale/amux/panes_20-10`: `615,543,624 ns/op`
  - `amux/test (benchmark-only rerun)` / `test` / `BenchmarkSplitScale/amux/panes_10-10`: `338,154,417 ns/op`
  - `amux/test (benchmark-only rerun)` / `test` / `BenchmarkThroughputPersistent/amux-10`: `251,451,943 ns/op`
  - `amux/test (benchmark-only rerun)` / `test` / `BenchmarkThroughput/amux-10`: `211,898,490 ns/op`
  - `amux/test (benchmark-only rerun)` / `test` / `BenchmarkSplitScale/tmux/panes_20-10`: `173,639,411 ns/op`
  - `amux/test (benchmark-only rerun)` / `test` / `BenchmarkSplitScale/amux/panes_4-10`: `104,478,600 ns/op`
  - `amux/test (benchmark-only rerun)` / `test` / `BenchmarkOutputDetection/polling/panes_4-10`: `85,167,688 ns/op`
  - `amux/test (benchmark-only rerun)` / `test` / `BenchmarkSplitScale/tmux/panes_10-10`: `71,474,062 ns/op`
  - `amux/test (benchmark-only rerun)` / `test` / `BenchmarkInputLatency/amux-10`: `51,814,636 ns/op`
- Largest allocation medians:
  - `amux/test (benchmark-only rerun)` / `test` / `BenchmarkSplitScale/amux/panes_20-10`: `103,204,656 B/op`, `164,497 allocs/op`
  - `amux/client` / `internal/client` / `BenchmarkRendererHandleLayout/panes_20-10`: `83,257,668 B/op`, `8,271 allocs/op`
  - `amux/test (benchmark-only rerun)` / `test` / `BenchmarkSplitScale/amux/panes_10-10`: `50,594,618 B/op`, `76,999 allocs/op`
  - `amux/mux` / `internal/mux` / `BenchmarkPaneApplyOutput/bytes_32768-10`: `41,850,557 B/op`, `23,130 allocs/op`
  - `amux/client` / `internal/client` / `BenchmarkRendererHandleLayout/panes_10-10`: `39,940,464 B/op`, `3,917 allocs/op`
  - `amux/render` / `internal/render` / `BenchmarkRenderFull/panes_1-10`: `25,711,786 B/op`, `47,147 allocs/op`
  - `amux/test (benchmark-only rerun)` / `test` / `BenchmarkSplitScale/amux/panes_4-10`: `18,455,629 B/op`, `26,671 allocs/op`
  - `amux/client` / `internal/client` / `BenchmarkRendererHandleLayout/panes_4-10`: `13,821,921 B/op`, `1,342 allocs/op`
  - `amux/render` / `internal/render` / `BenchmarkRenderDiffDirtyPanes/full_redraw_one_of_twenty-10`: `11,581,295 B/op`, `43,941 allocs/op`
  - `amux/render` / `internal/render` / `BenchmarkRenderFull/panes_4-10`: `10,443,085 B/op`, `45,173 allocs/op`

## Full Results

| Suite | Package | Benchmark | ns/op | B/op | allocs/op | Fits 8.33ms? |
| --- | --- | --- | ---: | ---: | ---: | --- |
| amux/client | internal/client | `BenchmarkRendererCaptureJSON/panes_1/build-10` | 49,291 | 23,032 | 296 | yes |
| amux/client | internal/client | `BenchmarkRendererCaptureJSON/panes_1/marshal-10` | 19,555 | 20,608 | 4 | yes |
| amux/client | internal/client | `BenchmarkRendererCaptureJSON/panes_20/build-10` | 517,146 | 392,400 | 5,830 | yes |
| amux/client | internal/client | `BenchmarkRendererCaptureJSON/panes_20/marshal-10` | 349,365 | 475,301 | 5 | yes |
| amux/client | internal/client | `BenchmarkRendererHandleLayout/panes_10-10` | 933,930 | 39,940,464 | 3,917 | yes |
| amux/client | internal/client | `BenchmarkRendererHandleLayout/panes_20-10` | 2,863,786 | 83,257,668 | 8,271 | yes |
| amux/client | internal/client | `BenchmarkRendererHandleLayout/panes_4-10` | 401,311 | 13,821,921 | 1,342 | yes |
| amux/client | internal/client | `BenchmarkRendererHandlePaneOutput/bytes_256-10` | 79,917 | 20,846 | 171 | yes |
| amux/client | internal/client | `BenchmarkRendererHandlePaneOutput/bytes_32768-10` | 9,978,635 | 2,673,120 | 22,077 | no |
| amux/client | internal/client | `BenchmarkRendererHandlePaneOutput/bytes_4096-10` | 1,266,055 | 335,327 | 2,768 | yes |
| amux/mux | internal/mux | `BenchmarkClose/panes_10-10` | 6,840 | 9,456 | 78 | yes |
| amux/mux | internal/mux | `BenchmarkClose/panes_2-10` | 668 | 1,584 | 6 | yes |
| amux/mux | internal/mux | `BenchmarkClose/panes_4-10` | 1,745 | 3,552 | 24 | yes |
| amux/mux | internal/mux | `BenchmarkEmulatorContentLines/Render+StripANSI-10` | 82,431 | 10,612 | 274 | yes |
| amux/mux | internal/mux | `BenchmarkEmulatorContentLines/ScreenLineText-10` | 12,683 | 2,304 | 25 | yes |
| amux/mux | internal/mux | `BenchmarkEmulatorRender-10` | 75,908 | 7,000 | 177 | yes |
| amux/mux | internal/mux | `BenchmarkEmulatorWrite/bytes_256-10` | 71,937 | 20,669 | 168 | yes |
| amux/mux | internal/mux | `BenchmarkEmulatorWrite/bytes_32768-10` | 9,409,167 | 2,671,651 | 22,074 | no |
| amux/mux | internal/mux | `BenchmarkEmulatorWrite/bytes_4096-10` | 1,047,238 | 334,900 | 2,765 | yes |
| amux/mux | internal/mux | `BenchmarkFixOffsets/panes_1-10` | 2.417 | 0 | 0 | yes |
| amux/mux | internal/mux | `BenchmarkFixOffsets/panes_10-10` | 44.5 | 0 | 0 | yes |
| amux/mux | internal/mux | `BenchmarkFixOffsets/panes_20-10` | 69.5 | 0 | 0 | yes |
| amux/mux | internal/mux | `BenchmarkFixOffsets/panes_4-10` | 17.8 | 0 | 0 | yes |
| amux/mux | internal/mux | `BenchmarkPaneApplyOutput/bytes_256-10` | 88,367 | 319,718 | 180 | yes |
| amux/mux | internal/mux | `BenchmarkPaneApplyOutput/bytes_32768-10` | 13,235,053 | 41,850,557 | 23,130 | no |
| amux/mux | internal/mux | `BenchmarkPaneApplyOutput/bytes_4096-10` | 1,432,388 | 5,415,177 | 2,899 | yes |
| amux/mux | internal/mux | `BenchmarkResolvePane/panes_1-10` | 74.1 | 64 | 3 | yes |
| amux/mux | internal/mux | `BenchmarkResolvePane/panes_10-10` | 467 | 872 | 11 | yes |
| amux/mux | internal/mux | `BenchmarkResolvePane/panes_20-10` | 767 | 1,656 | 12 | yes |
| amux/mux | internal/mux | `BenchmarkScreenContains/Render+StripANSI+Contains-10` | 79,590 | 10,634 | 186 | yes |
| amux/mux | internal/mux | `BenchmarkScreenContains/ScreenContains-10` | 22,783 | 3,968 | 25 | yes |
| amux/mux | internal/mux | `BenchmarkSplit/depth_1-10` | 258 | 736 | 2 | yes |
| amux/mux | internal/mux | `BenchmarkSplit/depth_10-10` | 5,979 | 8,000 | 35 | yes |
| amux/mux | internal/mux | `BenchmarkSplit/depth_4-10` | 1,297 | 3,280 | 14 | yes |
| amux/mux | internal/mux | `BenchmarkSplitIncremental/siblings_10-10` | 4,112 | 8,080 | 31 | yes |
| amux/mux | internal/mux | `BenchmarkSplitIncremental/siblings_20-10` | 6,677 | 16,976 | 62 | yes |
| amux/mux | internal/mux | `BenchmarkSplitIncremental/siblings_4-10` | 1,192 | 3,088 | 11 | yes |
| amux/mux | internal/mux | `BenchmarkSplitIncremental/siblings_40-10` | 13,420 | 37,232 | 123 | yes |
| amux/mux | internal/mux | `BenchmarkWalk/panes_1-10` | 2.542 | 0 | 0 | yes |
| amux/mux | internal/mux | `BenchmarkWalk/panes_10-10` | 41.8 | 0 | 0 | yes |
| amux/mux | internal/mux | `BenchmarkWalk/panes_20-10` | 57.6 | 0 | 0 | yes |
| amux/mux | internal/mux | `BenchmarkWalk/panes_4-10` | 19.7 | 0 | 0 | yes |
| amux/proto | internal/proto | `BenchmarkWriteReadMsgPaneOutput-10` | 210 | 736 | 8 | yes |
| amux/render | internal/render | `BenchmarkBuildBorderMap/panes_10-10` | 16,536 | 225,361 | 13 | yes |
| amux/render | internal/render | `BenchmarkBuildBorderMap/panes_2-10` | 17,095 | 217,169 | 12 | yes |
| amux/render | internal/render | `BenchmarkBuildBorderMap/panes_20-10` | 17,215 | 225,360 | 13 | yes |
| amux/render | internal/render | `BenchmarkBuildBorderMap/panes_4-10` | 19,356 | 225,361 | 13 | yes |
| amux/render | internal/render | `BenchmarkClipLine/width_200-10` | 642 | 49 | 1 | yes |
| amux/render | internal/render | `BenchmarkClipLine/width_40-10` | 182 | 49 | 1 | yes |
| amux/render | internal/render | `BenchmarkClipLine/width_80-10` | 298 | 49 | 1 | yes |
| amux/render | internal/render | `BenchmarkRenderDiffDirtyPanes/dirty_one_of_twenty-10` | 406,241 | 1,428,247 | 118 | yes |
| amux/render | internal/render | `BenchmarkRenderDiffDirtyPanes/full_redraw_one_of_twenty-10` | 29,696,754 | 11,581,295 | 43,941 | no |
| amux/render | internal/render | `BenchmarkRenderFull/panes_1-10` | 41,411,485 | 25,711,786 | 47,147 | no |
| amux/render | internal/render | `BenchmarkRenderFull/panes_10-10` | 22,620,896 | 10,198,873 | 44,713 | no |
| amux/render | internal/render | `BenchmarkRenderFull/panes_20-10` | 22,827,358 | 10,200,428 | 44,793 | no |
| amux/render | internal/render | `BenchmarkRenderFull/panes_4-10` | 23,787,087 | 10,443,085 | 45,173 | no |
| amux/server | internal/server | `BenchmarkReadMsg_Layout/panes_1-10` | 38,382 | 32,368 | 682 | yes |
| amux/server | internal/server | `BenchmarkReadMsg_Layout/panes_10-10` | 51,519 | 35,728 | 700 | yes |
| amux/server | internal/server | `BenchmarkReadMsg_Layout/panes_20-10` | 41,860 | 39,792 | 720 | yes |
| amux/server | internal/server | `BenchmarkReadMsg_PaneOutput/bytes_256-10` | 279 | 640 | 5 | yes |
| amux/server | internal/server | `BenchmarkReadMsg_PaneOutput/bytes_32768-10` | 3,121 | 33,152 | 5 | yes |
| amux/server | internal/server | `BenchmarkReadMsg_PaneOutput/bytes_4096-10` | 643 | 4,480 | 5 | yes |
| amux/server | internal/server | `BenchmarkSessionBroadcastLayout/panes_1-10` | 16,961 | 11,005 | 59 | yes |
| amux/server | internal/server | `BenchmarkSessionBroadcastLayout/panes_20-10` | 30,200 | 60,221 | 96 | yes |
| amux/server | internal/server | `BenchmarkSessionBroadcastLayout/panes_4-10` | 22,743 | 14,909 | 74 | yes |
| amux/server | internal/server | `BenchmarkSessionSnapshotLayout/panes_1-10` | 375 | 800 | 6 | yes |
| amux/server | internal/server | `BenchmarkSessionSnapshotLayout/panes_20-10` | 12,717 | 40,224 | 38 | yes |
| amux/server | internal/server | `BenchmarkSessionSnapshotLayout/panes_4-10` | 1,620 | 4,640 | 20 | yes |
| amux/server | internal/server | `BenchmarkWriteMsg_Layout/panes_1-10` | 29,458 | 9,501 | 47 | yes |
| amux/server | internal/server | `BenchmarkWriteMsg_Layout/panes_10-10` | 19,447 | 9,501 | 47 | yes |
| amux/server | internal/server | `BenchmarkWriteMsg_Layout/panes_20-10` | 18,875 | 10,397 | 48 | yes |
| amux/server | internal/server | `BenchmarkWriteMsg_PaneOutput/bytes_256-10` | 29.9 | 16 | 1 | yes |
| amux/server | internal/server | `BenchmarkWriteMsg_PaneOutput/bytes_32768-10` | 724 | 16 | 1 | yes |
| amux/server | internal/server | `BenchmarkWriteMsg_PaneOutput/bytes_4096-10` | 77.7 | 16 | 1 | yes |
| amux/test (benchmark-only rerun) | test | `BenchmarkCapture/amux/panes_1-10` | 18,366,134 | 299,948 | 1,154 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkCapture/amux/panes_4-10` | 12,845,603 | 318,924 | 1,868 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkCapture/tmux/panes_1-10` | 5,948,807 | 15,699 | 59 | yes |
| amux/test (benchmark-only rerun) | test | `BenchmarkCapture/tmux/panes_4-10` | 4,485,275 | 15,687 | 59 | yes |
| amux/test (benchmark-only rerun) | test | `BenchmarkCaptureScale/amux/panes_1-10` | 8,822,459 | 2,127,336 | 1,688 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkCaptureScale/amux/panes_10-10` | 11,249,612 | 2,030,698 | 6,516 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkCaptureScale/amux/panes_20-10` | 9,540,109 | 1,955,792 | 11,800 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkCaptureScale/amux/panes_4-10` | 8,911,909 | 2,085,894 | 3,345 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkCaptureScale/tmux/panes_1-10` | 4,272,099 | 15,703 | 59 | yes |
| amux/test (benchmark-only rerun) | test | `BenchmarkCaptureScale/tmux/panes_10-10` | 4,476,558 | 15,689 | 59 | yes |
| amux/test (benchmark-only rerun) | test | `BenchmarkCaptureScale/tmux/panes_20-10` | 4,166,366 | 15,694 | 59 | yes |
| amux/test (benchmark-only rerun) | test | `BenchmarkCaptureScale/tmux/panes_4-10` | 4,574,748 | 15,685 | 59 | yes |
| amux/test (benchmark-only rerun) | test | `BenchmarkDetectLayoutChange/polling-10` | 46,135,857 | 236,179 | 1,398 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkDetectLayoutChange/push-10` | 325,329 | 114,006 | 2,274 | yes |
| amux/test (benchmark-only rerun) | test | `BenchmarkHotReload-10` | 1,653,608,250 | 142,072 | 4,094 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkInputLatency/amux-10` | 51,814,636 | 93,321 | 1,603 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkInputLatency/tmux-10` | 11,869,369 | 32,104 | 124 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkList/amux/panes_1-10` | 7,210,429 | 15,111 | 59 | yes |
| amux/test (benchmark-only rerun) | test | `BenchmarkList/amux/panes_4-10` | 5,852,695 | 17,597 | 60 | yes |
| amux/test (benchmark-only rerun) | test | `BenchmarkList/tmux/panes_1-10` | 5,209,634 | 15,692 | 59 | yes |
| amux/test (benchmark-only rerun) | test | `BenchmarkList/tmux/panes_4-10` | 5,880,300 | 15,876 | 59 | yes |
| amux/test (benchmark-only rerun) | test | `BenchmarkOutputDetection/polling/panes_1-10` | 49,607,398 | 147,195 | 1,102 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkOutputDetection/polling/panes_4-10` | 85,167,688 | 414,987 | 1,994 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkOutputDetection/push/panes_1-10` | 379,594 | 110,853 | 2,208 | yes |
| amux/test (benchmark-only rerun) | test | `BenchmarkOutputDetection/push/panes_4-10` | 446,452 | 119,084 | 2,385 | yes |
| amux/test (benchmark-only rerun) | test | `BenchmarkSendKeys/amux/panes_1-10` | 208,124 | 46,325 | 769 | yes |
| amux/test (benchmark-only rerun) | test | `BenchmarkSendKeys/amux/panes_4-10` | 217,535 | 43,079 | 742 | yes |
| amux/test (benchmark-only rerun) | test | `BenchmarkSendKeys/tmux/panes_1-10` | 4,152,957 | 14,592 | 57 | yes |
| amux/test (benchmark-only rerun) | test | `BenchmarkSendKeys/tmux/panes_4-10` | 6,112,844 | 14,592 | 57 | yes |
| amux/test (benchmark-only rerun) | test | `BenchmarkSplitScale/amux/panes_10-10` | 338,154,417 | 50,594,618 | 76,999 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkSplitScale/amux/panes_20-10` | 615,543,624 | 103,204,656 | 164,497 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkSplitScale/amux/panes_4-10` | 104,478,600 | 18,455,629 | 26,671 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkSplitScale/tmux/panes_10-10` | 71,474,062 | 137,376 | 531 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkSplitScale/tmux/panes_20-10` | 173,639,411 | 293,616 | 1,131 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkSplitScale/tmux/panes_4-10` | 24,505,186 | 43,632 | 171 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkThroughput/amux-10` | 211,898,490 | 4,537,686 | 48,530 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkThroughput/tmux-10` | 50,201,659 | 79,279 | 302 | no |
| amux/test (benchmark-only rerun) | test | `BenchmarkThroughputPersistent/amux-10` | 251,451,943 | 4,542,128 | 48,576 | no |
| ultraviolet | root | `BenchmarkBufferResize-10` | 13,515 | 27,099 | 2 | yes |
| ultraviolet | root | `BenchmarkBufferSetCell-10` | 4.457 | 0 | 0 | yes |
| ultraviolet | root | `BenchmarkDetectSequenceMap-10` | 337,167 | 151,540 | 3,228 | yes |
| ultraviolet | root | `BenchmarkRenderBufferAreaOps/clear/full-10` | 7,546 | 0 | 0 | yes |
| ultraviolet | root | `BenchmarkRenderBufferAreaOps/clear/partial-10` | 728 | 0 | 0 | yes |
| ultraviolet | root | `BenchmarkRenderBufferAreaOps/fill/full-10` | 7,876 | 0 | 0 | yes |
| ultraviolet | root | `BenchmarkRenderBufferAreaOps/fill/partial-10` | 818 | 0 | 0 | yes |
| ultraviolet | root | `BenchmarkRenderBufferSetCell/changed-10` | 12.5 | 0 | 0 | yes |
| ultraviolet | root | `BenchmarkRenderBufferSetCell/noop-10` | 12.4 | 0 | 0 | yes |
| ultraviolet | root | `BenchmarkStyledStringDrawScreenBuffer-10` | 31,791 | 130 | 2 | yes |
| ultraviolet | root | `BenchmarkStyledStringDrawVariants/screenbuffer/ansi-10` | 30,405 | 129 | 2 | yes |
| ultraviolet | root | `BenchmarkStyledStringDrawVariants/screenbuffer/plain-10` | 32,332 | 130 | 2 | yes |
| ultraviolet | root | `BenchmarkStyledStringDrawVariants/terminalscreen/ansi-10` | 65,066 | 112 | 1 | yes |
| ultraviolet | root | `BenchmarkStyledStringDrawVariants/terminalscreen/plain-10` | 46,726 | 117 | 1 | yes |
| ultraviolet | root | `BenchmarkTerminalScreenRenderSparseUpdates-10` | 96,151 | 5,124 | 180 | yes |
| ultraviolet | root | `BenchmarkTerminalScreenSetCell/changed-10` | 54.9 | 0 | 0 | yes |
| ultraviolet | root | `BenchmarkTerminalScreenSetCell/noop-10` | 36.6 | 0 | 0 | yes |
| ultraviolet | layout | `BenchmarkLayout_Split/with_cache-10` | 419 | 40 | 4 | yes |
| ultraviolet | layout | `BenchmarkLayout_Split/without_cache-10` | 67,016 | 32,066 | 221 | yes |
| ultraviolet | screen | `BenchmarkClear-10` | 7,539 | 16 | 1 | yes |
| ultraviolet | screen | `BenchmarkClone-10` | 195,093 | 228,008 | 27 | yes |
| ultraviolet | screen | `BenchmarkCloneArea-10` | 10,109 | 23,320 | 13 | yes |
| ultraviolet | screen | `BenchmarkFill-10` | 8,076 | 16 | 1 | yes |
