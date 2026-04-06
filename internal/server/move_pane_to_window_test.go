package server

import (
	"fmt"
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

func testPane(id uint32) *mux.Pane {
	return newProxyPane(id, mux.PaneMeta{Name: fmt.Sprintf(mux.PaneNameFormat, id)}, 80, 22, nil, nil, nil)
}

func TestMovePaneToWindowMovesPaneAcrossWindows(t *testing.T) {
	t.Parallel()

	sess := newSession("test")
	t.Cleanup(func() { stopSessionBackgroundLoops(t, sess) })

	sourcePane := testPane(1)
	sourcePeer := testPane(2)
	targetPane := testPane(3)

	source := mux.NewWindow(sourcePane, 80, 23)
	source.ID = 1
	source.Name = "editor"
	if _, err := source.Split(mux.SplitVertical, sourcePeer); err != nil {
		t.Fatalf("source split: %v", err)
	}

	target := mux.NewWindow(targetPane, 80, 23)
	target.ID = 2
	target.Name = "logs"

	setSessionLayoutForTest(t, sess, source.ID, []*mux.Window{source, target}, sourcePane, sourcePeer, targetPane)

	res := toCommandResult(sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		if err := movePaneToWindow(mctx, 1, "1", "2"); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{broadcastLayout: true}
	}))
	if res.Err != nil {
		t.Fatalf("move-pane-to-window error = %v", res.Err)
	}

	if got := source.Root.FindPane(2); got == nil {
		t.Fatal("source window should keep pane-2")
	}
	if got := source.Root.FindPane(1); got != nil {
		t.Fatal("source window should no longer contain pane-1")
	}
	if got := target.Root.FindPane(1); got == nil {
		t.Fatal("target window should contain moved pane-1")
	}
	if got := target.Root.FindPane(3); got == nil {
		t.Fatal("target window should keep existing pane-3")
	}
}

func TestMovePaneToWindowClosesSourceWindowWhenLastPaneMoves(t *testing.T) {
	t.Parallel()

	sess := newSession("test")
	t.Cleanup(func() { stopSessionBackgroundLoops(t, sess) })

	sourcePane := testPane(1)
	targetPane := testPane(2)

	source := mux.NewWindow(sourcePane, 80, 23)
	source.ID = 1
	source.Name = "editor"

	target := mux.NewWindow(targetPane, 80, 23)
	target.ID = 2
	target.Name = "logs"

	setSessionLayoutForTest(t, sess, source.ID, []*mux.Window{source, target}, sourcePane, targetPane)

	res := toCommandResult(sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		if err := movePaneToWindow(mctx, 1, "1", "2"); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{broadcastLayout: true}
	}))
	if res.Err != nil {
		t.Fatalf("move-pane-to-window error = %v", res.Err)
	}

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		windows int
		active  uint32
		source  *mux.Window
	} {
		return struct {
			windows int
			active  uint32
			source  *mux.Window
		}{
			windows: len(sess.Windows),
			active:  sess.ActiveWindowID,
			source:  sess.windowByID(source.ID),
		}
	})
	if state.windows != 1 {
		t.Fatalf("windows = %d, want 1", state.windows)
	}
	if state.active != target.ID {
		t.Fatalf("active window = %d, want %d", state.active, target.ID)
	}
	if state.source != nil {
		t.Fatal("source window should be removed after moving its last pane")
	}
	if got := target.Root.FindPane(1); got == nil {
		t.Fatal("target window should contain moved pane-1")
	}
}

func TestMovePaneToWindowSameWindowIsNoOp(t *testing.T) {
	t.Parallel()

	sess := newSession("test")
	t.Cleanup(func() { stopSessionBackgroundLoops(t, sess) })

	p1 := testPane(1)
	p2 := testPane(2)
	window := mux.NewWindow(p1, 80, 23)
	window.ID = 1
	window.Name = "editor"
	if _, err := window.Split(mux.SplitVertical, p2); err != nil {
		t.Fatalf("split: %v", err)
	}

	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, p1, p2)

	res := toCommandResult(sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		if err := movePaneToWindow(mctx, 1, "1", "1"); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{broadcastLayout: true}
	}))
	if res.Err != nil {
		t.Fatalf("move-pane-to-window error = %v", res.Err)
	}

	if got := mustSessionQuery(t, sess, func(sess *Session) int { return len(sess.Windows) }); got != 1 {
		t.Fatalf("windows = %d, want 1", got)
	}
	if got := window.Root.FindPane(1); got == nil {
		t.Fatal("pane-1 should remain in the same window")
	}
	if got := window.Root.FindPane(2); got == nil {
		t.Fatal("pane-2 should remain in the same window")
	}
}
