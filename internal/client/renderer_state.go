package client

import (
	"fmt"
	"strings"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

type rendererSnapshot struct {
	emulators       map[uint32]mux.TerminalEmulator
	paneInfo        map[uint32]proto.PaneSnapshot
	layout          *mux.LayoutCell
	activePaneID    uint32
	zoomedPaneID    uint32
	sessionName     string
	sessionNotice   string
	width           int
	height          int
	windows         []proto.WindowSnapshot
	activeWinID     uint32
	scrollbackLines int
}

func newRendererSnapshot(width, height, scrollbackLines int) *rendererSnapshot {
	return &rendererSnapshot{
		emulators:       make(map[uint32]mux.TerminalEmulator),
		paneInfo:        make(map[uint32]proto.PaneSnapshot),
		width:           width,
		height:          height,
		scrollbackLines: scrollbackLines,
	}
}

func (s *rendererSnapshot) layoutFingerprint() string {
	if s.layout == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d,%d,%d;", s.width, s.height, s.zoomedPaneID)
	s.layout.Walk(func(cell *mux.LayoutCell) {
		fmt.Fprintf(&b, "%d:%d,%d,%d,%d;", cell.CellPaneID(), cell.X, cell.Y, cell.W, cell.H)
	})
	return b.String()
}

func (s *rendererSnapshot) captureRoot(layoutHeight int) (*mux.LayoutCell, uint32) {
	root := s.layout
	if s.zoomedPaneID != 0 {
		root = mux.NewLeafByID(s.zoomedPaneID, 0, 0, s.width, layoutHeight)
	}
	return root, s.activePaneID
}

func (s *rendererSnapshot) visibleLayout(layoutHeight int) *mux.LayoutCell {
	if s.layout == nil {
		return nil
	}
	if s.zoomedPaneID != 0 {
		return mux.NewLeafByID(s.zoomedPaneID, 0, 0, s.width, layoutHeight)
	}
	return s.layout
}

func cloneWindowSnapshots(src []proto.WindowSnapshot) []proto.WindowSnapshot {
	return append([]proto.WindowSnapshot(nil), src...)
}

type rendererCommand struct {
	run  func(*rendererActorState)
	done chan struct{}
}

type rendererActorState struct {
	snapshot   *rendererSnapshot
	compositor *render.Compositor
}

func (r *Renderer) loadSnapshot() *rendererSnapshot {
	return r.state.Load()
}

func (r *Renderer) snapshot() *rendererSnapshot {
	return r.loadSnapshot()
}

func (r *Renderer) publishSnapshot(snap *rendererSnapshot) {
	r.state.Store(snap)
}

func (r *Renderer) withActor(run func(*rendererActorState)) {
	done := make(chan struct{})
	r.commands <- rendererCommand{run: run, done: done}
	<-done
}

func (r *Renderer) actorLoop(initial *rendererSnapshot, width, height int) {
	state := &rendererActorState{
		snapshot:   initial,
		compositor: render.NewCompositor(width, height, ""),
	}
	for cmd := range r.commands {
		cmd.run(state)
		close(cmd.done)
	}
}

func withRendererActorValue[T any](r *Renderer, run func(*rendererActorState) T) T {
	done := make(chan struct{})
	var value T
	r.commands <- rendererCommand{
		run: func(st *rendererActorState) {
			value = run(st)
		},
		done: done,
	}
	<-done
	return value
}
