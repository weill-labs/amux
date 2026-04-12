package server

import (
	"reflect"
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

func TestCommandSpawnAutoAppendsToShortestUnderfilledColumnAndEqualizes(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	p3 := newTestPane(sess, 3, "pane-3")
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "main"
	if _, err := w.SplitPaneWithOptions(p1.ID, mux.SplitHorizontal, p2, mux.SplitOptions{}); err != nil {
		t.Fatalf("Split pane-1 horizontally: %v", err)
	}
	if _, err := w.SplitRoot(mux.SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot pane-3: %v", err)
	}
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1, p2, p3)

	res := runTestCommand(t, srv, sess, "spawn", "--auto", "--name", "worker-4")
	if res.cmdErr != "" {
		t.Fatalf("spawn --auto failed: %s", res.cmdErr)
	}

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		p1X int
		p2X int
		p3X int
		p4X int
		p3H int
		p4H int
	} {
		w := sess.activeWindow()
		p4, err := sess.findPaneByRef("worker-4")
		if err != nil {
			return struct {
				p1X int
				p2X int
				p3X int
				p4X int
				p3H int
				p4H int
			}{}
		}
		return struct {
			p1X int
			p2X int
			p3X int
			p4X int
			p3H int
			p4H int
		}{
			p1X: w.Root.FindPane(p1.ID).X,
			p2X: w.Root.FindPane(p2.ID).X,
			p3X: w.Root.FindPane(p3.ID).X,
			p4X: w.Root.FindPane(p4.ID).X,
			p3H: w.Root.FindPane(p3.ID).H,
			p4H: w.Root.FindPane(p4.ID).H,
		}
	})

	if state.p1X != state.p2X {
		t.Fatalf("auto spawn should keep pane-1 and pane-2 in the original column: %+v", state)
	}
	if state.p3X != state.p4X {
		t.Fatalf("auto spawn should place pane-3 and worker-4 in the shortest column: %+v", state)
	}
	if state.p1X == state.p4X {
		t.Fatalf("auto spawn should leave pane-1/pane-2 in a different column from worker-4: %+v", state)
	}
	if state.p3H != state.p4H {
		t.Fatalf("auto spawn should equalize row heights in the filled column: %+v", state)
	}
}

func TestCommandSpawnAutoRootSplitsWhenColumnsAreFull(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "main"
	if _, err := w.SplitPaneWithOptions(p1.ID, mux.SplitHorizontal, p2, mux.SplitOptions{}); err != nil {
		t.Fatalf("Split pane-1 horizontally: %v", err)
	}
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1, p2)

	res := runTestCommand(t, srv, sess, "spawn", "--auto", "--name", "worker-3")
	if res.cmdErr != "" {
		t.Fatalf("spawn --auto failed: %s", res.cmdErr)
	}

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		p1X int
		p2X int
		p3X int
		p3H int
	} {
		w := sess.activeWindow()
		p3, err := sess.findPaneByRef("worker-3")
		if err != nil {
			return struct {
				p1X int
				p2X int
				p3X int
				p3H int
			}{}
		}
		return struct {
			p1X int
			p2X int
			p3X int
			p3H int
		}{
			p1X: w.Root.FindPane(p1.ID).X,
			p2X: w.Root.FindPane(p2.ID).X,
			p3X: w.Root.FindPane(p3.ID).X,
			p3H: w.Root.FindPane(p3.ID).H,
		}
	})

	if state.p1X != state.p2X {
		t.Fatalf("pane-1 and pane-2 should remain stacked in the original column: %+v", state)
	}
	if state.p3X == state.p1X {
		t.Fatalf("worker-3 should be placed in a new root column: %+v", state)
	}
	if state.p3H != 23 {
		t.Fatalf("new root column should span the full window height, got %+v", state)
	}
}

func TestCommandSpawnAutoRootSplitsAndEqualizesLeadColumn(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	p3 := newTestPane(sess, 3, "pane-3")
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "main"
	if _, err := w.SplitRoot(mux.SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot pane-2: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead pane-1: %v", err)
	}
	if _, err := w.SplitPaneWithOptions(p2.ID, mux.SplitHorizontal, p3, mux.SplitOptions{}); err != nil {
		t.Fatalf("Split pane-2 horizontally: %v", err)
	}
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1, p2, p3)

	res := runTestCommand(t, srv, sess, "spawn", "--auto", "--name", "worker-4")
	if res.cmdErr != "" {
		t.Fatalf("spawn --auto failed: %s", res.cmdErr)
	}

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		p1X int
		p2X int
		p3X int
		p4X int
		p1W int
		p2W int
		p3W int
		p4W int
	} {
		w := sess.activeWindow()
		p4, err := sess.findPaneByRef("worker-4")
		if err != nil {
			return struct {
				p1X int
				p2X int
				p3X int
				p4X int
				p1W int
				p2W int
				p3W int
				p4W int
			}{}
		}
		p1Cell := w.Root.FindPane(p1.ID)
		p2Cell := w.Root.FindPane(p2.ID)
		p3Cell := w.Root.FindPane(p3.ID)
		p4Cell := w.Root.FindPane(p4.ID)
		if p1Cell == nil || p2Cell == nil || p3Cell == nil || p4Cell == nil {
			return struct {
				p1X int
				p2X int
				p3X int
				p4X int
				p1W int
				p2W int
				p3W int
				p4W int
			}{}
		}
		return struct {
			p1X int
			p2X int
			p3X int
			p4X int
			p1W int
			p2W int
			p3W int
			p4W int
		}{
			p1X: p1Cell.X,
			p2X: p2Cell.X,
			p3X: p3Cell.X,
			p4X: p4Cell.X,
			p1W: p1Cell.W,
			p2W: p2Cell.W,
			p3W: p3Cell.W,
			p4W: p4Cell.W,
		}
	})

	if state.p2X != state.p3X {
		t.Fatalf("pane-2 and pane-3 should remain stacked in the middle column: %+v", state)
	}
	if state.p1X >= state.p2X || state.p2X >= state.p4X {
		t.Fatalf("lead, logical, and spawned columns should remain left-to-right: %+v", state)
	}
	if got := []int{state.p1W, state.p2W, state.p3W, state.p4W}; !reflect.DeepEqual(got, []int{26, 26, 26, 26}) {
		t.Fatalf("spawn --auto should equalize widths across lead and non-lead columns: %v", got)
	}
}

func TestCommandSpawnAutoTargetsSpecifiedWindow(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	p3 := newTestPane(sess, 3, "pane-3")
	p4 := newTestPane(sess, 4, "pane-4")

	mainWindow := mux.NewWindow(p1, 80, 23)
	mainWindow.ID = 1
	mainWindow.Name = "main"

	logsWindow := mux.NewWindow(p2, 80, 23)
	logsWindow.ID = 4
	logsWindow.Name = "logs"
	if _, err := logsWindow.SplitPaneWithOptions(p2.ID, mux.SplitHorizontal, p3, mux.SplitOptions{}); err != nil {
		t.Fatalf("Split pane-2 horizontally: %v", err)
	}
	if _, err := logsWindow.SplitRoot(mux.SplitVertical, p4); err != nil {
		t.Fatalf("SplitRoot pane-4: %v", err)
	}

	setSessionLayoutForTest(t, sess, mainWindow.ID, []*mux.Window{mainWindow, logsWindow}, p1, p2, p3, p4)

	res := runTestCommand(t, srv, sess, "spawn", "--auto", "--window", "4", "--name", "worker-5")
	if res.cmdErr != "" {
		t.Fatalf("spawn --auto --window failed: %s", res.cmdErr)
	}

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		activeWindowID uint32
		workerWindowID uint32
		p2X            int
		p3X            int
		p4X            int
		p5X            int
		p4H            int
		p5H            int
	} {
		worker, err := sess.findPaneByRef("worker-5")
		if err != nil {
			return struct {
				activeWindowID uint32
				workerWindowID uint32
				p2X            int
				p3X            int
				p4X            int
				p5X            int
				p4H            int
				p5H            int
			}{}
		}
		workerWindow := sess.findWindowByPaneID(worker.ID)
		p2Cell := logsWindow.Root.FindPane(p2.ID)
		p3Cell := logsWindow.Root.FindPane(p3.ID)
		p4Cell := logsWindow.Root.FindPane(p4.ID)
		p5Cell := logsWindow.Root.FindPane(worker.ID)
		if workerWindow == nil || p2Cell == nil || p3Cell == nil || p4Cell == nil || p5Cell == nil {
			return struct {
				activeWindowID uint32
				workerWindowID uint32
				p2X            int
				p3X            int
				p4X            int
				p5X            int
				p4H            int
				p5H            int
			}{}
		}
		return struct {
			activeWindowID uint32
			workerWindowID uint32
			p2X            int
			p3X            int
			p4X            int
			p5X            int
			p4H            int
			p5H            int
		}{
			activeWindowID: sess.ActiveWindowID,
			workerWindowID: workerWindow.ID,
			p2X:            p2Cell.X,
			p3X:            p3Cell.X,
			p4X:            p4Cell.X,
			p5X:            p5Cell.X,
			p4H:            p4Cell.H,
			p5H:            p5Cell.H,
		}
	})

	if state.activeWindowID != mainWindow.ID {
		t.Fatalf("active window = %d, want %d", state.activeWindowID, mainWindow.ID)
	}
	if state.workerWindowID != logsWindow.ID {
		t.Fatalf("worker window = %d, want %d", state.workerWindowID, logsWindow.ID)
	}
	if state.p2X != state.p3X {
		t.Fatalf("pane-2 and pane-3 should remain in the original logs column: %+v", state)
	}
	if state.p4X != state.p5X {
		t.Fatalf("auto spawn should place pane-4 and worker-5 in the shortest logs column: %+v", state)
	}
	if state.p2X == state.p5X {
		t.Fatalf("worker-5 should be placed in a different logs column from pane-2/pane-3: %+v", state)
	}
	if state.p4H != state.p5H {
		t.Fatalf("auto spawn should equalize row heights in the targeted window column: %+v", state)
	}
}

func TestQueryCreatePaneSnapshotColumnFillPaneHintErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setup   func(t *testing.T, sess *Session) uint32
		paneRef string
		wantErr string
	}{
		{
			name: "missing pane ref",
			setup: func(t *testing.T, sess *Session) uint32 {
				t.Helper()

				p1 := newTestPane(sess, 1, "pane-1")
				mainWindow := mux.NewWindow(p1, 80, 23)
				mainWindow.ID = 1
				mainWindow.Name = "main"
				setSessionLayoutForTest(t, sess, mainWindow.ID, []*mux.Window{mainWindow}, p1)
				return p1.ID
			},
			paneRef: "missing",
			wantErr: `pane "missing" not found`,
		},
		{
			name: "pane not in any window",
			setup: func(t *testing.T, sess *Session) uint32 {
				t.Helper()

				p1 := newTestPane(sess, 1, "pane-1")
				orphan := newTestPane(sess, 2, "orphan-pane")
				mainWindow := mux.NewWindow(p1, 80, 23)
				mainWindow.ID = 1
				mainWindow.Name = "main"
				setSessionLayoutForTest(t, sess, mainWindow.ID, []*mux.Window{mainWindow}, p1, orphan)
				return p1.ID
			},
			paneRef: "orphan-pane",
			wantErr: "pane not in any window",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, sess, cleanup := newCommandTestSession(t)
			defer cleanup()

			actorPaneID := tt.setup(t, sess)
			_, err := queryCreatePaneSnapshot(sess, actorPaneID, "spawn", createPanePlacementColumnFill, tt.paneRef, "")
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("queryCreatePaneSnapshot(..., %q) error = %v, want %q", tt.paneRef, err, tt.wantErr)
			}
		})
	}
}

func TestCommandSpawnAutoUsesPaneTargetWindowHint(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	p3 := newTestPane(sess, 3, "pane-3")
	p4 := newTestPane(sess, 4, "pane-4")

	mainWindow := mux.NewWindow(p1, 80, 23)
	mainWindow.ID = 1
	mainWindow.Name = "main"

	logsWindow := mux.NewWindow(p2, 80, 23)
	logsWindow.ID = 4
	logsWindow.Name = "logs"
	if _, err := logsWindow.SplitPaneWithOptions(p2.ID, mux.SplitHorizontal, p3, mux.SplitOptions{}); err != nil {
		t.Fatalf("Split pane-2 horizontally: %v", err)
	}
	if _, err := logsWindow.SplitRoot(mux.SplitVertical, p4); err != nil {
		t.Fatalf("SplitRoot pane-4: %v", err)
	}

	setSessionLayoutForTest(t, sess, mainWindow.ID, []*mux.Window{mainWindow, logsWindow}, p1, p2, p3, p4)

	res := runTestCommand(t, srv, sess, "spawn", "--auto", "--at", "pane-2", "--name", "worker-5")
	if res.cmdErr != "" {
		t.Fatalf("spawn --auto --at failed: %s", res.cmdErr)
	}

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		activeWindowID uint32
		workerWindowID uint32
		p2X            int
		p3X            int
		p4X            int
		p5X            int
		p4H            int
		p5H            int
	} {
		worker, err := sess.findPaneByRef("worker-5")
		if err != nil {
			return struct {
				activeWindowID uint32
				workerWindowID uint32
				p2X            int
				p3X            int
				p4X            int
				p5X            int
				p4H            int
				p5H            int
			}{}
		}
		workerWindow := sess.findWindowByPaneID(worker.ID)
		p2Cell := logsWindow.Root.FindPane(p2.ID)
		p3Cell := logsWindow.Root.FindPane(p3.ID)
		p4Cell := logsWindow.Root.FindPane(p4.ID)
		p5Cell := logsWindow.Root.FindPane(worker.ID)
		if workerWindow == nil || p2Cell == nil || p3Cell == nil || p4Cell == nil || p5Cell == nil {
			return struct {
				activeWindowID uint32
				workerWindowID uint32
				p2X            int
				p3X            int
				p4X            int
				p5X            int
				p4H            int
				p5H            int
			}{}
		}
		return struct {
			activeWindowID uint32
			workerWindowID uint32
			p2X            int
			p3X            int
			p4X            int
			p5X            int
			p4H            int
			p5H            int
		}{
			activeWindowID: sess.ActiveWindowID,
			workerWindowID: workerWindow.ID,
			p2X:            p2Cell.X,
			p3X:            p3Cell.X,
			p4X:            p4Cell.X,
			p5X:            p5Cell.X,
			p4H:            p4Cell.H,
			p5H:            p5Cell.H,
		}
	})

	if state.activeWindowID != mainWindow.ID {
		t.Fatalf("active window = %d, want %d", state.activeWindowID, mainWindow.ID)
	}
	if state.workerWindowID != logsWindow.ID {
		t.Fatalf("worker window = %d, want %d", state.workerWindowID, logsWindow.ID)
	}
	if state.p2X != state.p3X {
		t.Fatalf("pane-2 and pane-3 should remain in the original logs column: %+v", state)
	}
	if state.p4X != state.p5X {
		t.Fatalf("auto spawn should place pane-4 and worker-5 in the shortest logs column: %+v", state)
	}
	if state.p2X == state.p5X {
		t.Fatalf("worker-5 should be placed in a different logs column from pane-2/pane-3: %+v", state)
	}
	if state.p4H != state.p5H {
		t.Fatalf("auto spawn should equalize row heights in the pane-targeted window column: %+v", state)
	}
}

func TestCommandSpawnTargetsSpecifiedWindowActivePane(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	p3 := newTestPane(sess, 3, "pane-3")

	mainWindow := mux.NewWindow(p1, 80, 23)
	mainWindow.ID = 1
	mainWindow.Name = "main"

	logsWindow := mux.NewWindow(p2, 80, 23)
	logsWindow.ID = 2
	logsWindow.Name = "logs"
	if _, err := logsWindow.SplitPaneWithOptions(p2.ID, mux.SplitHorizontal, p3, mux.SplitOptions{}); err != nil {
		t.Fatalf("Split pane-2 horizontally: %v", err)
	}
	logsWindow.FocusPane(p2)

	setSessionLayoutForTest(t, sess, mainWindow.ID, []*mux.Window{mainWindow, logsWindow}, p1, p2, p3)

	res := runTestCommand(t, srv, sess, "spawn", "--window", "logs", "--name", "worker-4")
	if res.cmdErr != "" {
		t.Fatalf("spawn --window failed: %s", res.cmdErr)
	}

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		activeWindowID uint32
		workerWindowID uint32
		workerX        int
		workerY        int
		p2X            int
		p2Y            int
		p3Y            int
	} {
		worker, err := sess.findPaneByRef("worker-4")
		if err != nil {
			return struct {
				activeWindowID uint32
				workerWindowID uint32
				workerX        int
				workerY        int
				p2X            int
				p2Y            int
				p3Y            int
			}{}
		}
		workerWindow := sess.findWindowByPaneID(worker.ID)
		workerCell := logsWindow.Root.FindPane(worker.ID)
		p2Cell := logsWindow.Root.FindPane(p2.ID)
		p3Cell := logsWindow.Root.FindPane(p3.ID)
		if workerWindow == nil || workerCell == nil || p2Cell == nil || p3Cell == nil {
			return struct {
				activeWindowID uint32
				workerWindowID uint32
				workerX        int
				workerY        int
				p2X            int
				p2Y            int
				p3Y            int
			}{}
		}
		return struct {
			activeWindowID uint32
			workerWindowID uint32
			workerX        int
			workerY        int
			p2X            int
			p2Y            int
			p3Y            int
		}{
			activeWindowID: sess.ActiveWindowID,
			workerWindowID: workerWindow.ID,
			workerX:        workerCell.X,
			workerY:        workerCell.Y,
			p2X:            p2Cell.X,
			p2Y:            p2Cell.Y,
			p3Y:            p3Cell.Y,
		}
	})

	if state.activeWindowID != mainWindow.ID {
		t.Fatalf("active window = %d, want %d", state.activeWindowID, mainWindow.ID)
	}
	if state.workerWindowID != logsWindow.ID {
		t.Fatalf("worker window = %d, want %d", state.workerWindowID, logsWindow.ID)
	}
	if state.workerX != state.p2X {
		t.Fatalf("worker-4 should split the targeted window active pane column: %+v", state)
	}
	if state.workerY <= state.p2Y {
		t.Fatalf("worker-4 should be placed below pane-2 after the split: %+v", state)
	}
	if state.workerY >= state.p3Y {
		t.Fatalf("worker-4 should split pane-2, not pane-3: %+v", state)
	}
}
