# Directional Pane Focus (LAB-147) Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace amux's distance-based directional focus with tmux's adjacency + overlap + wrapping + recency algorithm, and add prefix + arrow key support.

**Architecture:** Add `ActivePoint` counter to `Pane` for recency tracking. Rewrite `Focus()` directional logic to use strict edge-adjacency with perpendicular overlap, wrapping to the opposite edge when no adjacent candidates exist. Extend the prefix key handler in `main.go` to buffer escape sequences for arrow key detection.

**Tech Stack:** Go, existing `mux` package

**Spec:** `docs/superpowers/specs/2026-03-15-directional-focus-design.md`

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/mux/pane.go` | Modify | Add `ActivePoint uint64` field to `Pane` struct |
| `internal/mux/window.go` | Modify | Rewrite `Focus()` directional logic, add `activePointCounter` |
| `internal/mux/window_test.go` | Modify | Rewrite directional focus tests for new semantics, add wrapping/recency tests |
| `main.go` | Modify | Handle escape sequences in prefix mode for arrow keys |
| `test/focus_test.go` | Modify | Add integration test for prefix + arrow key focus |

---

## Chunk 1: Core Algorithm

### Task 1: Add ActivePoint to Pane

**Files:**
- Modify: `internal/mux/pane.go:33-48` (Pane struct)

- [ ] **Step 1: Add ActivePoint field**

In `internal/mux/pane.go`, add `ActivePoint uint64` to the `Pane` struct:

```go
type Pane struct {
	ID          uint32
	ActivePoint uint64 // monotonic counter — higher means more recently focused
	Meta        PaneMeta
	// ... rest unchanged
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/cweill/github/amux7 && go build ./...`
Expected: SUCCESS (new field is zero-valued by default)

- [ ] **Step 3: Commit**

```bash
git add internal/mux/pane.go
git commit -m "mux: add ActivePoint field to Pane for recency tracking (LAB-147)"
```

### Task 2: Rewrite Focus() directional logic

**Files:**
- Modify: `internal/mux/window.go:205-345` (Focus function and overlap helpers)

- [ ] **Step 1: Write failing tests for new adjacency + wrapping behavior**

Replace the directional focus tests in `internal/mux/window_test.go`. The existing tests (lines 46-173) assume distance-based selection; the new tests assert adjacency + recency + wrapping:

```go
func TestFocusUpAdjacent(t *testing.T) {
	t.Parallel()
	// Two panes stacked vertically, separated by a 1-cell border.
	//   pane 1: (0,0)  40x12
	//   border: y=12
	//   pane 2: (0,13) 40x12  <- active
	w := buildLayout(2, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 40, 12},
		{2, 0, 13, 40, 12},
	})

	w.Focus("up")

	if w.ActivePane.ID != 1 {
		t.Errorf("Focus(up) = pane %d, want pane 1", w.ActivePane.ID)
	}
}

func TestFocusUpWraps(t *testing.T) {
	t.Parallel()
	// Active pane is at top — up should wrap to the bottom pane.
	//   pane 1: (0,0)  40x12  <- active
	//   border: y=12
	//   pane 2: (0,13) 40x12
	w := buildLayout(1, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 40, 12},
		{2, 0, 13, 40, 12},
	})
	w.Width = 40
	w.Height = 25 // 12 + 1 border + 12

	w.Focus("up")

	if w.ActivePane.ID != 2 {
		t.Errorf("Focus(up) from top = pane %d, want pane 2 (wrap)", w.ActivePane.ID)
	}
}

func TestFocusDownWraps(t *testing.T) {
	t.Parallel()
	// Active pane is at bottom — down should wrap to the top pane.
	w := buildLayout(2, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 40, 12},
		{2, 0, 13, 40, 12},
	})
	w.Width = 40
	w.Height = 25

	w.Focus("down")

	if w.ActivePane.ID != 1 {
		t.Errorf("Focus(down) from bottom = pane %d, want pane 1 (wrap)", w.ActivePane.ID)
	}
}

func TestFocusLeftWraps(t *testing.T) {
	t.Parallel()
	// Two panes side by side. Active is leftmost — left should wrap to right.
	w := buildLayout(1, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 39, 24},
		{2, 40, 0, 39, 24},
	})
	w.Width = 79 // 39 + 1 border + 39
	w.Height = 24

	w.Focus("left")

	if w.ActivePane.ID != 2 {
		t.Errorf("Focus(left) from leftmost = pane %d, want pane 2 (wrap)", w.ActivePane.ID)
	}
}

func TestFocusRightWraps(t *testing.T) {
	t.Parallel()
	// Two panes side by side. Active is rightmost — right should wrap to left.
	w := buildLayout(2, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 39, 24},
		{2, 40, 0, 39, 24},
	})
	w.Width = 79
	w.Height = 24

	w.Focus("right")

	if w.ActivePane.ID != 1 {
		t.Errorf("Focus(right) from rightmost = pane %d, want pane 1 (wrap)", w.ActivePane.ID)
	}
}

func TestFocusRecencyTiebreaker(t *testing.T) {
	t.Parallel()
	// Three panes: two above active, both adjacent with overlap.
	// The one with higher ActivePoint should win.
	//
	//   pane 1: (0,0)  40x10
	//   pane 2: (0,0)  40x10   (same position — both above, both overlap)
	//   pane 3: (0,11) 40x10  <- active
	//
	// We simulate this with two panes at different X but both overlapping:
	//   pane 1: (0,0)   30x10  — overlaps active's X range
	//   pane 2: (10,0)  30x10  — overlaps active's X range, higher ActivePoint
	//   pane 3: (0,11)  40x10  <- active
	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	p1.ActivePoint = 5
	p2.ActivePoint = 10 // more recent

	leaves := []*LayoutCell{
		NewLeaf(p1, 0, 0, 30, 10),
		NewLeaf(p2, 10, 0, 30, 10),
		NewLeaf(p3, 0, 11, 40, 10),
	}
	root := &LayoutCell{
		X: 0, Y: 0, W: 40, H: 21,
		Dir:      SplitVertical,
		Children: leaves,
	}
	for _, l := range leaves {
		l.Parent = root
	}
	w := &Window{Root: root, ActivePane: p3, Width: 40, Height: 21}

	w.Focus("up")

	if w.ActivePane.ID != 2 {
		t.Errorf("Focus(up) recency = pane %d, want pane 2 (higher ActivePoint)", w.ActivePane.ID)
	}
}

func TestFocusNoOverlapSkipped(t *testing.T) {
	t.Parallel()
	// Two panes: one above but with NO horizontal overlap, one to the side.
	// Up should NOT match a non-overlapping pane adjacent above
	// (it should wrap instead).
	//
	//   pane 1: (50,0)  30x10   — above but columns 50-79 (no overlap with active 0-39)
	//   pane 2: (0,11)  40x10   <- active (columns 0-39)
	w := buildLayout(2, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 50, 0, 30, 10},
		{2, 0, 11, 40, 10},
	})
	w.Width = 80
	w.Height = 21

	w.Focus("up")

	// No adjacent pane with overlap above, wrapping finds pane 1 at bottom edge?
	// Actually pane 1 is not at the bottom edge either. With only 2 panes and
	// no overlap in either direction, focus should still try wrapping.
	// In tmux, if no adjacent+overlapping candidate exists at all (even after wrap),
	// it's a no-op.
	// But our buildLayout has Window dimensions 200x200 by default.
	// Let's just verify the algorithm doesn't crash and stays on pane 2.
	if w.ActivePane.ID != 2 {
		t.Errorf("Focus(up) no-overlap = pane %d, want pane 2 (no-op)", w.ActivePane.ID)
	}
}

func TestFocusActivePointIncremented(t *testing.T) {
	t.Parallel()
	// Verify that Focus() increments ActivePoint on the new active pane.
	w := buildLayout(1, []struct {
		id         uint32
		x, y, w, h int
	}{
		{1, 0, 0, 40, 12},
		{2, 0, 13, 40, 12},
	})
	w.Width = 40
	w.Height = 25

	before := w.ActivePane.ActivePoint
	w.Focus("down")

	if w.ActivePane.ActivePoint <= before {
		t.Errorf("ActivePoint not incremented: got %d, want > %d", w.ActivePane.ActivePoint, before)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/cweill/github/amux7 && go test ./internal/mux/ -run 'TestFocus(Up|Down|Left|Right|Recency|NoOverlap|ActivePoint)' -v`
Expected: FAIL (existing Focus() uses distance, not adjacency/wrapping)

- [ ] **Step 3: Implement the new Focus() algorithm**

Replace `Focus()` directional logic and overlap helpers in `internal/mux/window.go`:

```go
// activePointCounter is a package-level monotonic counter for pane focus recency.
var activePointCounter uint64

// Focus changes the active pane. Direction is "next", "left", "right", "up", "down".
// Auto-unzooms if a pane is zoomed.
func (w *Window) Focus(direction string) {
	if w.ZoomedPaneID != 0 {
		w.Unzoom()
	}
	panes := w.Panes()
	if len(panes) <= 1 {
		return
	}

	if direction == "next" {
		for i, p := range panes {
			if p.ID == w.ActivePane.ID {
				w.ActivePane = panes[(i+1)%len(panes)]
				activePointCounter++
				w.ActivePane.ActivePoint = activePointCounter
				return
			}
		}
		return
	}

	activeCell := w.Root.FindPane(w.ActivePane.ID)
	if activeCell == nil {
		return
	}

	// Try adjacent panes, then wrap to opposite edge.
	best := w.findDirectional(activeCell, direction, false)
	if best == nil {
		best = w.findDirectional(activeCell, direction, true)
	}

	if best != nil {
		w.ActivePane = best.Pane
		activePointCounter++
		w.ActivePane.ActivePoint = activePointCounter
	}
}

// findDirectional finds the best pane in the given direction from activeCell.
// If wrap is true, searches from the opposite window edge instead.
func (w *Window) findDirectional(activeCell *LayoutCell, direction string, wrap bool) *LayoutCell {
	// Compute the edge to search along and the perpendicular range.
	var edge int
	var rangeStart, rangeEnd int

	switch direction {
	case "up":
		edge = activeCell.Y
		if wrap {
			edge = w.Height + 1
		}
		rangeStart = activeCell.X
		rangeEnd = activeCell.X + activeCell.W
	case "down":
		edge = activeCell.Y + activeCell.H + 1
		if wrap {
			edge = 0
		}
		rangeStart = activeCell.X
		rangeEnd = activeCell.X + activeCell.W
	case "left":
		edge = activeCell.X
		if wrap {
			edge = w.Width + 1
		}
		rangeStart = activeCell.Y
		rangeEnd = activeCell.Y + activeCell.H
	case "right":
		edge = activeCell.X + activeCell.W + 1
		if wrap {
			edge = 0
		}
		rangeStart = activeCell.Y
		rangeEnd = activeCell.Y + activeCell.H
	}

	var best *LayoutCell
	var bestActivePoint uint64

	w.Root.Walk(func(cell *LayoutCell) {
		if cell.Pane == nil || cell.Pane.ID == w.ActivePane.ID {
			return
		}

		// Check adjacency: candidate's far edge must be exactly at our edge.
		adjacent := false
		var candStart, candEnd int
		switch direction {
		case "up":
			adjacent = cell.Y+cell.H+1 == edge
			candStart = cell.X
			candEnd = cell.X + cell.W
		case "down":
			adjacent = cell.Y == edge
			candStart = cell.X
			candEnd = cell.X + cell.W
		case "left":
			adjacent = cell.X+cell.W+1 == edge
			candStart = cell.Y
			candEnd = cell.Y + cell.H
		case "right":
			adjacent = cell.X == edge
			candStart = cell.Y
			candEnd = cell.Y + cell.H
		}

		if !adjacent {
			return
		}

		// Check perpendicular overlap (inclusive intersection).
		if candStart >= rangeEnd || candEnd <= rangeStart {
			return
		}

		// Tiebreaker: most recently active pane wins.
		if best == nil || cell.Pane.ActivePoint > bestActivePoint {
			best = cell
			bestActivePoint = cell.Pane.ActivePoint
		}
	})

	return best
}
```

Remove the old `overlapsY` and `overlapsX` helpers (lines 337-345) since they're no longer used.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/cweill/github/amux7 && go test ./internal/mux/ -v`
Expected: ALL PASS

- [ ] **Step 5: Run full test suite**

Run: `cd /Users/cweill/github/amux7 && go test ./...`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add internal/mux/window.go internal/mux/window_test.go
git commit -m "mux: rewrite Focus() to use tmux-style adjacency + wrapping + recency (LAB-147)"
```

---

## Chunk 2: Prefix + Arrow Keys

### Task 3: Add prefix + arrow key support

**Files:**
- Modify: `main.go:385-458` (processKeyByte function)

- [ ] **Step 1: Write integration test for prefix + arrow keys**

Add to `test/focus_test.go`:

```go
func TestPrefixArrowFocus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.startAmux()
	h.splitPane()       // Ctrl-a \  → two panes side by side
	h.sendKeys("C-a", "h") // focus left (existing hjkl)
	h.assertActivePaneIs(t, "pane-1")

	// Now test arrow keys: Ctrl-a then Left arrow
	h.sendKeys("C-a", "Left")
	// Should wrap from leftmost to rightmost
	h.assertActivePaneIs(t, "pane-2")
}
```

Note: The exact integration test API depends on the existing harness patterns. Check `test/focus_test.go` for existing focus tests and adapt.

- [ ] **Step 2: Implement escape sequence buffering in prefix mode**

In `main.go`, modify `processKeyByte` to handle escape sequences when in prefix mode. When prefix is true and `\x1b` arrives, set a `prefixEsc` flag and buffer bytes. On the next bytes `[` + `A/B/C/D`, dispatch to the corresponding focus direction:

```go
// Add these variables alongside the existing `prefix` bool:
var prefixEsc bool    // true when we've seen \x1b in prefix mode
var prefixEscBuf []byte

// In processKeyByte, before the existing prefix handling:
if prefixEsc {
    prefixEscBuf = append(prefixEscBuf, b)
    if len(prefixEscBuf) == 1 && b == '[' {
        // Waiting for the direction byte
        return false
    }
    if len(prefixEscBuf) == 2 && prefixEscBuf[0] == '[' {
        prefixEsc = false
        switch b {
        case 'A':
            sendCommand(conn, "focus", []string{"up"})
        case 'B':
            sendCommand(conn, "focus", []string{"down"})
        case 'C':
            sendCommand(conn, "focus", []string{"right"})
        case 'D':
            sendCommand(conn, "focus", []string{"left"})
        default:
            // Not an arrow key — forward the buffered bytes as raw input
            *forward = append(*forward, 0x01) // re-send prefix
            *forward = append(*forward, 0x1b) // the escape
            *forward = append(*forward, prefixEscBuf...)
        }
        prefixEscBuf = nil
        return false
    }
    // Unexpected sequence — flush
    prefixEsc = false
    *forward = append(*forward, 0x01, 0x1b)
    *forward = append(*forward, prefixEscBuf...)
    prefixEscBuf = nil
    return false
}

// Then modify the existing prefix == true block to handle \x1b:
// In the switch b { ... } under prefix mode, add:
case 0x1b:
    prefixEsc = true
    prefixEscBuf = nil
```

- [ ] **Step 3: Run integration tests**

Run: `cd /Users/cweill/github/amux7 && go test -v -run TestPrefixArrow ./test/ -timeout 30s`
Expected: PASS

- [ ] **Step 4: Run full test suite**

Run: `cd /Users/cweill/github/amux7 && go test ./...`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add main.go test/focus_test.go
git commit -m "client: support prefix + arrow keys for directional focus (LAB-147)"
```

---

## Chunk 3: Cleanup and Verify

### Task 4: End-to-end verification

- [ ] **Step 1: Build and install**

Run: `cd /Users/cweill/github/amux7 && go build -o ~/.local/bin/amux .`

- [ ] **Step 2: Manual smoke test**

```bash
amux
# Split: Ctrl-a \
# Split again: Ctrl-a \
# Focus left: Ctrl-a h → should move to left pane
# Focus right: Ctrl-a l → should move to right pane
# Focus right from rightmost: Ctrl-a l → should WRAP to leftmost
# Focus with arrow: Ctrl-a → → should focus right
# Focus with arrow: Ctrl-a ← → should focus left
```

- [ ] **Step 3: Run full test suite one final time**

Run: `cd /Users/cweill/github/amux7 && go test ./... -count=1`
Expected: ALL PASS
