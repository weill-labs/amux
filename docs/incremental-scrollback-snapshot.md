# Incremental Scrollback Capture Snapshot

## Problem

`publishPaneCapture` runs on the renderer actor for every pane-output event.
Today it calls `capturePaneRenderSnapshot`, which rebuilds the full text
scrollback by walking every retained row with `ScrollbackLineText`. On a
5000-line scrollback this makes the actor spend measurable CPU on capture-only
state even when no capture request is active.

The captured profile for LAB-1833 shows `capturePaneRenderSnapshot` at 7.11s
cumulative out of 14.97s total samples, with `ScrollbackLineText` alone at
3.63s cumulative. The per-output render path does not consume
`paneRenderSnapshot.scrollback`; only capture snapshot readers do.

## VT Scrollback Invariants

The project imports `github.com/charmbracelet/x/vt`, replaced by
`github.com/weill-labs/x/vt v0.0.0-20260514062200-6ca40b8a9268`.

The fork's `Scrollback` uses `scrollbackRing`:

- `Push` appends a line at the logical back.
- When the ring is full, `Push` overwrites the oldest slot and advances
  `start`, so logical trimming happens at the front.
- `Line(0)` returns the oldest line and `Line(Len()-1)` returns the most recent
  line.
- `SetMaxLines` keeps the newest `min(currentLen, maxLines)` rows.
- `Clear` resets the ring to empty.

The fork exposes `ScrollbackPush(count, width)` and `ScrollbackClear()`
callbacks, but it does not expose a cumulative pushed counter. amux already
sets these callbacks in `mux.(*vtEmulator)`, so LAB-1833 can add a small
counter in the mux wrapper without changing the vt fork.

## Design

Add `ScrollbackPushed() uint64` to `mux.TerminalEmulator`.
`mux.(*vtEmulator)` increments this counter from the vt `ScrollbackPush`
callback by the callback's `count`. `Reset` and vt `ScrollbackClear` reset the
retained scrollback, but they do not reset this counter; the renderer can model
a clear as all previous rows being trimmed while the cumulative push count stays
monotonic.

The renderer actor keeps one incremental scrollback state per pane:

- previous scrollback length
- previous cumulative pushed counter
- previous immutable `[]paneBufferLine` text snapshot

On each pane snapshot publish:

1. Read `curLen := emu.ScrollbackLen()`.
2. Read `curPushed := emu.ScrollbackPushed()`.
3. Compute `appended := curPushed - prevPushed`.
4. Compute `trimmed := (prevLen + appended) - curLen`.
5. If `appended == 0 && trimmed == 0`, reuse the previous slice header.
6. Otherwise reslice away `trimmed` front rows and append only the `appended`
   newest rows from the emulator.

The newest appended rows are read from indices
`curLen - appended` through `curLen - 1`.

## Safety

`paneBufferLine.text` is a Go string, so the copied scrollback text is immutable.
Existing snapshot cloning already shallow-copies `paneRenderSnapshot` values and
documents that `scrollback` and `screen` slices are immutable after capture.
Tail-sharing the previous scrollback slice with `prev[trimmed:]` preserves that
contract.

The cold-start path performs the existing full rebuild. The implementation also
falls back to a full rebuild if counters move backwards, if trim math is
negative, if the previous slice is missing, or if the previous slice length does
not match the recorded previous length. These cases should not happen during
normal actor-owned emulator updates, but the fallback keeps capture correctness
ahead of optimization.

## Test Plan

- Unit-test the incremental builder for cold start, no-change, appended-only,
  trimmed-only, appended-and-trimmed, and defensive fallback.
- Unit-test `ScrollbackPushed()` on `vtEmulator`: it starts at zero, never
  decreases, and increments by the number of rows pushed into scrollback.
- Benchmark a full 5000-line scrollback followed by a one-line append per
  snapshot.
- Re-run the actor-free capture invariant test for
  `TestHandleCaptureRequestDoesNotWaitForRendererActor`.
- Verify touched client and mux tests with `-count=100`, targeted race runs,
  and the requested package slice.
