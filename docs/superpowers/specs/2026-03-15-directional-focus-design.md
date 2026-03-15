# Directional Pane Focus — tmux-style (LAB-147)

## Motivation

amux supports `Ctrl-a h/j/k/l` for directional pane focus, but the algorithm diverges from tmux: it uses center-point distance for candidate selection and nearest-in-direction fallback without wrapping. Users expect tmux-style behavior: strict adjacency, perpendicular overlap, edge wrapping, and most-recently-active tiebreaker. Additionally, prefix + arrow keys (`Ctrl-a ↑/↓/←/→`) don't work because the prefix handler only processes single bytes.

## Algorithm

Port tmux's directional focus algorithm (based on tmux 3.5a, `window.c:1388-1610`):

### 1. Edge calculation and candidate check

In amux's layout model, a 1-cell separator border sits between adjacent panes (see `FixOffsets`). All ranges are half-open: a pane at `(X, Y)` with size `(W, H)` occupies columns `[X, X+W)` and rows `[Y, Y+H)`.

| Direction | Edge (normal) | Candidate adjacency check | Perpendicular overlap axis |
|-----------|--------------|--------------------------|---------------------------|
| Up | `active.Y` | `candidate.Y + candidate.H + 1 == edge` | X axis |
| Down | `active.Y + active.H + 1` | `candidate.Y == edge` | X axis |
| Left | `active.X` | `candidate.X + candidate.W + 1 == edge` | Y axis |
| Right | `active.X + active.W + 1` | `candidate.X == edge` | Y axis |

The `+1` in the adjacency check accounts for the 1-cell border separator between panes.

### 2. Perpendicular overlap filter

Among adjacent candidates, keep those with any overlap on the perpendicular axis. Using half-open interval intersection:

```
overlaps = (a_start < b_end) && (b_start < a_end)
```

For up/down: `[X, X+W)`. For left/right: `[Y, Y+H)`.

### 3. Tiebreaker: most recently active

Among remaining candidates, pick the one with the highest `ActivePoint` value. `ActivePoint` is a monotonic counter on `Pane`, incremented each time a pane receives focus.

### 4. Wrapping

If no candidates found at the normal edge, wrap to the opposite window edge and repeat:

| Direction | Wrapped edge |
|-----------|-------------|
| Up | `window.Height + 1` |
| Down | `0` |
| Left | `window.Width + 1` |
| Right | `0` |

### 5. No match

If still no candidates after wrapping, no-op.

## Changes

### `internal/mux/pane.go`
- Add `ActivePoint uint64` field to `Pane`.

### `internal/mux/window.go`
- Add a package-level `activePointCounter uint64` that increments on each focus change.
- Add a `setActive(*Pane)` helper that sets `ActivePane` and increments `ActivePoint`. Use it everywhere `ActivePane` is assigned: `Focus()`, `Split()`, `SplitRoot()`, `ClosePane()`, `Zoom()`.
- Rewrite the directional branch of `Focus()` to use the adjacency + overlap + wrapping algorithm.

### `internal/mux/window_test.go`
- Replace existing directional focus tests (which use artificial non-adjacent layouts) with tests reflecting adjacency semantics.
- The current "no overlap fallback" tests (`TestFocusUpNoOverlap`, etc.) use panes positioned with gaps — these are not representable in real split-tree layouts and are replaced with wrapping tests.
- Add tests for: wrapping (all 4 directions), recency tiebreaker, ActivePoint increment.

### `main.go`
- In the prefix key handler: when `\x1b` is received in prefix mode, enter an escape-buffering state. Subsequent bytes are buffered: if `[` followed by `A/B/C/D`, dispatch to the corresponding focus direction. Non-matching sequences are flushed as raw input. No timeout — arrow key bytes arrive together in the same read buffer in practice.

## Edge cases

- **Single pane:** `Focus()` returns early (already handled).
- **Zoomed pane:** Unzoom first (already handled).
- **Minimized panes:** Minimized panes have `H=1` (status line height). They participate in adjacency checks normally — their collapsed geometry means they'll only be adjacent to immediate neighbors.
- **Checkpoint/restore:** `ActivePoint` is a runtime-only recency counter, not serialized in `PaneCheckpoint`. After server reload, all panes start at `ActivePoint=0`, which is acceptable since focus history is ephemeral.

## Testing

- Unit tests in `window_test.go` for the algorithm (adjacency, overlap, wrapping, tiebreaker).
- Integration test in `test/` for prefix + arrow keys triggering directional focus.
