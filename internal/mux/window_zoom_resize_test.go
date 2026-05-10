package mux

import (
	"fmt"
	"testing"
)

type recordedResize struct {
	cols int
	rows int
}

func (r recordedResize) String() string {
	return fmt.Sprintf("%dx%d", r.cols, r.rows)
}

type resizeRecordingEmulator struct {
	TerminalEmulator
	cols    int
	rows    int
	changes []recordedResize
}

func newResizeRecordingPane(id uint32, cols, rows int) (*Pane, *resizeRecordingEmulator) {
	emu := &resizeRecordingEmulator{
		TerminalEmulator: NewVTEmulatorWithScrollback(cols, rows, DefaultScrollbackLines),
		cols:             cols,
		rows:             rows,
	}
	return &Pane{
		ID:       id,
		Meta:     PaneMeta{Name: fmt.Sprintf(PaneNameFormat, id), Host: DefaultHost},
		emulator: emu,
	}, emu
}

func (r *resizeRecordingEmulator) Resize(cols, rows int) {
	if r.cols != cols || r.rows != rows {
		r.changes = append(r.changes, recordedResize{cols: cols, rows: rows})
	}
	r.cols = cols
	r.rows = rows
	r.TerminalEmulator.Resize(cols, rows)
}

func (r *resizeRecordingEmulator) Size() (int, int) {
	return r.cols, r.rows
}

func (r *resizeRecordingEmulator) reset() {
	r.changes = nil
}

func TestZoomPreservingMutationsDoNotResizeZoomedPaneToHiddenCell(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(t *testing.T, w *Window, p3 *Pane)
	}{
		{
			name: "split active pane",
			mutate: func(t *testing.T, w *Window, p3 *Pane) {
				t.Helper()
				if _, err := w.SplitWithOptions(SplitVertical, p3, SplitOptions{KeepFocus: true}); err != nil {
					t.Fatalf("SplitWithOptions: %v", err)
				}
			},
		},
		{
			name: "split root",
			mutate: func(t *testing.T, w *Window, p3 *Pane) {
				t.Helper()
				if _, err := w.SplitRootWithOptions(SplitVertical, p3, SplitOptions{KeepFocus: true}); err != nil {
					t.Fatalf("SplitRootWithOptions: %v", err)
				}
			},
		},
		{
			name: "split subtree root",
			mutate: func(t *testing.T, w *Window, p3 *Pane) {
				t.Helper()
				if _, err := w.splitSubtreeRootWithOptions(w.Root, SplitVertical, p3, false, SplitOptions{KeepFocus: true}); err != nil {
					t.Fatalf("splitSubtreeRootWithOptions: %v", err)
				}
			},
		},
		{
			name: "replace hidden pane",
			mutate: func(t *testing.T, w *Window, p3 *Pane) {
				t.Helper()
				if err := w.ReplacePane(2, p3); err != nil {
					t.Fatalf("ReplacePane: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p1, recorder := newResizeRecordingPane(1, 80, 23)
			p2, _ := newResizeRecordingPane(2, 80, 23)
			p3, _ := newResizeRecordingPane(3, 80, 23)
			w := NewWindow(p1, 80, 24)
			if _, err := w.SplitRoot(SplitHorizontal, p2); err != nil {
				t.Fatalf("SplitRoot setup: %v", err)
			}
			if err := w.Zoom(p1.ID); err != nil {
				t.Fatalf("Zoom setup: %v", err)
			}
			recorder.reset()

			tt.mutate(t, w, p3)

			fullSize := recordedResize{cols: w.Width, rows: PaneContentHeight(w.Height)}
			for _, got := range recorder.changes {
				if got != fullSize {
					t.Fatalf("zoomed pane resize history = %v, want no hidden-cell resize; full size is %s", recorder.changes, fullSize)
				}
			}
			if w.ZoomedPaneID != p1.ID {
				t.Fatalf("ZoomedPaneID = %d, want %d", w.ZoomedPaneID, p1.ID)
			}
			if w.ActivePane != p1 {
				t.Fatalf("active pane = %v, want pane-1", w.ActivePane)
			}
			cell3 := w.Root.FindPane(p3.ID)
			if cell3 == nil {
				t.Fatal("pane-3 should be present in the layout")
			}
			if cols, rows := p3.EmulatorSize(); cols != cell3.W || rows != PaneContentHeight(cell3.H) {
				t.Fatalf("new pane size = %dx%d, want %dx%d", cols, rows, cell3.W, PaneContentHeight(cell3.H))
			}
		})
	}
}
