package mux

import (
	"os"
	"testing"
)

func markPaneResizeError(t *testing.T, pane *Pane) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe(): %v", err)
	}
	_ = w.Close()
	_ = r.Close()
	pane.ptmx = r
}

func mustSplitRootPane(t *testing.T, w *Window, dir SplitDir, pane *Pane) {
	t.Helper()
	if _, err := w.SplitRoot(dir, pane); err != nil {
		t.Fatalf("SplitRoot(%v): %v", dir, err)
	}
}

func mustSplitPane(t *testing.T, w *Window, paneID uint32, dir SplitDir, pane *Pane) {
	t.Helper()
	if _, err := w.SplitPaneWithOptions(paneID, dir, pane, SplitOptions{}); err != nil {
		t.Fatalf("SplitPaneWithOptions(%d, %v): %v", paneID, dir, err)
	}
}

func TestMarkPaneResizeErrorCausesPaneResizeFailure(t *testing.T) {
	t.Parallel()

	p := fakePaneID(1)
	markPaneResizeError(t, p)

	if err := p.Resize(10, 5); err == nil {
		t.Fatal("Resize() error = nil, want bad file descriptor")
	}
}

func TestWindowOperationsPropagateResizeFailures(t *testing.T) {
	t.Parallel()

	t.Run("unzoomed mutations surface resize errors", func(t *testing.T) {
		t.Run("split root with options", func(t *testing.T) {
			p1, p2 := fakePaneID(1), fakePaneID(2)
			w := NewWindow(p1, 80, 24)
			mustSplitRootPane(t, w, SplitVertical, p2)
			markPaneResizeError(t, p2)
			w.ZoomedPaneID = p2.ID

			if _, err := w.SplitRootWithOptions(SplitVertical, fakePaneID(3), SplitOptions{}); err == nil {
				t.Fatal("SplitRootWithOptions() error = nil, want resize failure")
			}
		})

		t.Run("split pane with options", func(t *testing.T) {
			p1, p2 := fakePaneID(1), fakePaneID(2)
			w := NewWindow(p1, 80, 24)
			mustSplitRootPane(t, w, SplitVertical, p2)
			markPaneResizeError(t, p2)
			w.ZoomedPaneID = p2.ID

			if _, err := w.SplitPaneWithOptions(p1.ID, SplitHorizontal, fakePaneID(3), SplitOptions{}); err == nil {
				t.Fatal("SplitPaneWithOptions() error = nil, want resize failure")
			}
		})

		t.Run("set lead", func(t *testing.T) {
			p1, p2 := fakePaneID(1), fakePaneID(2)
			w := NewWindow(p1, 80, 24)
			mustSplitRootPane(t, w, SplitVertical, p2)
			markPaneResizeError(t, p2)
			w.ZoomedPaneID = p2.ID

			if err := w.SetLead(p1.ID); err == nil {
				t.Fatal("SetLead() error = nil, want resize failure")
			}
		})

		t.Run("move pane to root edge", func(t *testing.T) {
			p1, p2 := fakePaneID(1), fakePaneID(2)
			w := NewWindow(p1, 80, 24)
			mustSplitRootPane(t, w, SplitVertical, p2)
			markPaneResizeError(t, p2)
			w.ZoomedPaneID = p2.ID

			if err := w.MovePaneToRootEdge(p1.ID, SplitHorizontal, false); err == nil {
				t.Fatal("MovePaneToRootEdge() error = nil, want resize failure")
			}
		})

		t.Run("swap tree", func(t *testing.T) {
			p1, p2, p3 := fakePaneID(1), fakePaneID(2), fakePaneID(3)
			w := NewWindow(p1, 120, 24)
			mustSplitRootPane(t, w, SplitVertical, p2)
			mustSplitRootPane(t, w, SplitVertical, p3)
			markPaneResizeError(t, p3)
			w.ZoomedPaneID = p3.ID

			if err := w.SwapTree(p1.ID, p2.ID); err == nil {
				t.Fatal("SwapTree() error = nil, want resize failure")
			}
		})

		t.Run("move pane between root groups", func(t *testing.T) {
			p1, p2, p3 := fakePaneID(1), fakePaneID(2), fakePaneID(3)
			w := NewWindow(p1, 120, 24)
			mustSplitRootPane(t, w, SplitVertical, p2)
			mustSplitRootPane(t, w, SplitVertical, p3)
			markPaneResizeError(t, p3)
			w.ZoomedPaneID = p3.ID

			if err := w.MovePane(p1.ID, p2.ID, true); err == nil {
				t.Fatal("MovePane() error = nil, want resize failure")
			}
		})

		t.Run("move pane between root groups with nested source group", func(t *testing.T) {
			p1, p2, p3 := fakePaneID(1), fakePaneID(2), fakePaneID(3)
			w := NewWindow(p1, 120, 24)
			mustSplitRootPane(t, w, SplitVertical, p2)
			mustSplitPane(t, w, p1.ID, SplitHorizontal, p3)
			if err := w.Zoom(p2.ID); err != nil {
				t.Fatalf("Zoom(%d): %v", p2.ID, err)
			}
			markPaneResizeError(t, p2)

			if err := w.MovePane(p1.ID, p2.ID, true); err == nil {
				t.Fatal("MovePane() error = nil, want resize failure while unzooming")
			}
		})

		t.Run("move pane into split unzooms first", func(t *testing.T) {
			p1, p2 := fakePaneID(1), fakePaneID(2)
			w := NewWindow(p1, 80, 24)
			mustSplitRootPane(t, w, SplitVertical, p2)
			if err := w.Zoom(p2.ID); err != nil {
				t.Fatalf("Zoom(%d): %v", p2.ID, err)
			}
			markPaneResizeError(t, p2)

			if err := w.MovePaneIntoSplit(p1.ID, p2.ID, SplitHorizontal, true); err == nil {
				t.Fatal("MovePaneIntoSplit() error = nil, want resize failure while unzooming")
			}
		})

		t.Run("move pane within split group", func(t *testing.T) {
			p1, p2, p3 := fakePaneID(1), fakePaneID(2), fakePaneID(3)
			w := NewWindow(p1, 120, 24)
			mustSplitRootPane(t, w, SplitVertical, p2)
			mustSplitPane(t, w, p2.ID, SplitHorizontal, p3)
			markPaneResizeError(t, p3)
			w.ZoomedPaneID = p3.ID

			if err := w.MovePaneDown(p2.ID); err == nil {
				t.Fatal("MovePaneDown() error = nil, want resize failure")
			}
		})

		t.Run("move pane to column", func(t *testing.T) {
			p1, p2, p3 := fakePaneID(1), fakePaneID(2), fakePaneID(3)
			w := NewWindow(p1, 120, 24)
			mustSplitRootPane(t, w, SplitVertical, p2)
			mustSplitRootPane(t, w, SplitVertical, p3)
			markPaneResizeError(t, p3)
			w.ZoomedPaneID = p3.ID

			if err := w.MovePaneToColumn(p1.ID, p2.ID); err == nil {
				t.Fatal("MovePaneToColumn() error = nil, want resize failure")
			}
		})

		t.Run("split subtree root with options", func(t *testing.T) {
			p1, p2 := fakePaneID(1), fakePaneID(2)
			w := NewWindow(p1, 80, 24)
			mustSplitRootPane(t, w, SplitVertical, p2)
			markPaneResizeError(t, p2)
			w.ZoomedPaneID = p2.ID

			if _, err := w.splitSubtreeRootWithOptions(w.Root, SplitHorizontal, fakePaneID(3), false, SplitOptions{}); err == nil {
				t.Fatal("splitSubtreeRootWithOptions() error = nil, want resize failure")
			}
		})
	})

	t.Run("pane resize failures during mutations", func(t *testing.T) {
		t.Run("split pane resizes new pane", func(t *testing.T) {
			p1 := fakePaneID(1)
			w := NewWindow(p1, 80, 24)
			p2 := fakePaneID(2)
			markPaneResizeError(t, p2)

			if _, err := w.SplitPaneWithOptions(p1.ID, SplitVertical, p2, SplitOptions{}); err == nil {
				t.Fatal("SplitPaneWithOptions() error = nil, want new-pane resize failure")
			}
		})

		t.Run("split pane resizes existing pane", func(t *testing.T) {
			p1 := fakePaneID(1)
			w := NewWindow(p1, 80, 24)
			markPaneResizeError(t, p1)

			if _, err := w.SplitPaneWithOptions(p1.ID, SplitVertical, fakePaneID(2), SplitOptions{}); err == nil {
				t.Fatal("SplitPaneWithOptions() error = nil, want existing-pane resize failure")
			}
		})

		t.Run("move pane into split resizes moved pane", func(t *testing.T) {
			p1, p2, p3 := fakePaneID(1), fakePaneID(2), fakePaneID(3)
			w := NewWindow(p1, 120, 24)
			mustSplitRootPane(t, w, SplitVertical, p2)
			mustSplitRootPane(t, w, SplitVertical, p3)
			markPaneResizeError(t, p2)

			if err := w.MovePaneIntoSplit(p2.ID, p3.ID, SplitHorizontal, false); err == nil {
				t.Fatal("MovePaneIntoSplit() error = nil, want moved-pane resize failure")
			}
		})

		t.Run("move pane into split resizes existing target pane", func(t *testing.T) {
			p1, p2, p3 := fakePaneID(1), fakePaneID(2), fakePaneID(3)
			w := NewWindow(p1, 120, 24)
			mustSplitRootPane(t, w, SplitVertical, p2)
			mustSplitRootPane(t, w, SplitVertical, p3)
			markPaneResizeError(t, p3)

			if err := w.MovePaneIntoSplit(p2.ID, p3.ID, SplitHorizontal, false); err == nil {
				t.Fatal("MovePaneIntoSplit() error = nil, want target-pane resize failure")
			}
		})

		t.Run("zoom and unzoom", func(t *testing.T) {
			p1, p2 := fakePaneID(1), fakePaneID(2)
			w := NewWindow(p1, 80, 24)
			mustSplitRootPane(t, w, SplitVertical, p2)
			markPaneResizeError(t, p2)

			if err := w.Zoom(p2.ID); err == nil {
				t.Fatal("Zoom() error = nil, want resize failure")
			}

			w.ZoomedPaneID = p2.ID
			if err := w.Unzoom(); err == nil {
				t.Fatal("Unzoom() error = nil, want resize failure")
			}
		})

		t.Run("zoom replaces existing zoom only after successful unzoom", func(t *testing.T) {
			p1, p2 := fakePaneID(1), fakePaneID(2)
			w := NewWindow(p1, 80, 24)
			mustSplitRootPane(t, w, SplitVertical, p2)
			if err := w.Zoom(p2.ID); err != nil {
				t.Fatalf("Zoom(%d): %v", p2.ID, err)
			}
			markPaneResizeError(t, p2)

			if err := w.Zoom(p1.ID); err == nil {
				t.Fatal("Zoom() error = nil, want resize failure while clearing prior zoom")
			}
		})

		t.Run("splice pane single", func(t *testing.T) {
			p1 := fakePaneID(1)
			w := NewWindow(p1, 80, 24)
			replacement := fakePaneID(2)
			markPaneResizeError(t, replacement)
			if _, err := w.SplicePane(p1.ID, []*Pane{replacement}); err == nil {
				t.Fatal("SplicePane(single) error = nil, want resize failure")
			}
		})

		t.Run("splice pane multi", func(t *testing.T) {
			p1 := fakePaneID(1)
			w := NewWindow(p1, 80, 24)
			p3, p4 := fakePaneID(3), fakePaneID(4)
			markPaneResizeError(t, p3)
			if _, err := w.SplicePane(p1.ID, []*Pane{p3, p4}); err == nil {
				t.Fatal("SplicePane(multi) error = nil, want resize failure")
			}
		})

		t.Run("unsplice pane", func(t *testing.T) {
			proxy := fakePaneID(1)
			proxy.Meta.Host = "gpu-box"
			proxy.writeOverride = func([]byte) (int, error) { return 0, nil }
			w := NewWindow(proxy, 80, 24)

			replacement := fakePaneID(2)
			markPaneResizeError(t, replacement)

			if err := w.UnsplicePane("gpu-box", replacement); err == nil {
				t.Fatal("UnsplicePane() error = nil, want resize failure")
			}
		})
	})
}

func TestWindowNoOpPathsOnUnzoomFailure(t *testing.T) {
	t.Parallel()

	t.Run("focus operations keep active pane", func(t *testing.T) {
		t.Run("directional focus", func(t *testing.T) {
			p1, p2 := fakePaneID(1), fakePaneID(2)
			w := NewWindow(p1, 80, 24)
			mustSplitRootPane(t, w, SplitVertical, p2)
			if err := w.Zoom(p2.ID); err != nil {
				t.Fatalf("Zoom(%d): %v", p2.ID, err)
			}
			markPaneResizeError(t, p2)
			before := w.ActivePane

			w.Focus("right")
			if w.ActivePane != before {
				t.Fatalf("Focus() active pane = %v, want unchanged %v", w.ActivePane, before)
			}
		})

		t.Run("direct focus", func(t *testing.T) {
			p1, p2 := fakePaneID(1), fakePaneID(2)
			w := NewWindow(p1, 80, 24)
			mustSplitRootPane(t, w, SplitVertical, p2)
			if err := w.Zoom(p2.ID); err != nil {
				t.Fatalf("Zoom(%d): %v", p2.ID, err)
			}
			markPaneResizeError(t, p2)
			before := w.ActivePane

			w.FocusPane(p1)
			if w.ActivePane != before {
				t.Fatalf("FocusPane() active pane = %v, want unchanged %v", w.ActivePane, before)
			}
		})
	})

	t.Run("resize returns false", func(t *testing.T) {
		p1, p2 := fakePaneID(1), fakePaneID(2)
		w := NewWindow(p1, 80, 24)
		mustSplitRootPane(t, w, SplitVertical, p2)
		markPaneResizeError(t, p2)
		w.ZoomedPaneID = p2.ID

		if got := w.ResizePane(p1.ID, "right", 3); got {
			t.Fatal("ResizePane() = true, want false on unzoom failure")
		}
	})

	t.Run("equalize returns false", func(t *testing.T) {
		p1, p2 := fakePaneID(1), fakePaneID(2)
		w := NewWindow(p1, 80, 24)
		mustSplitRootPane(t, w, SplitVertical, p2)
		markPaneResizeError(t, p2)
		w.ZoomedPaneID = p2.ID

		if got := w.Equalize(true, false); got {
			t.Fatal("Equalize() = true, want false on unzoom failure")
		}
	})
}
