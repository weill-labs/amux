package client

import (
	"fmt"
	"strings"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

type rendererSnapshot struct {
	paneInfo        map[uint32]proto.PaneSnapshot
	paneOrder       []uint32
	layout          *mux.LayoutCell
	visiblePaneIDs  map[uint32]struct{}
	activePaneID    uint32
	zoomedPaneID    uint32
	leadPaneID      uint32
	sessionName     string
	sessionNotice   string
	capabilities    proto.ClientCapabilities
	width           int
	height          int
	windows         []proto.WindowSnapshot
	activeWinID     uint32
	scrollbackLines int
}

func newRendererSnapshot(width, height, scrollbackLines int) *rendererSnapshot {
	return &rendererSnapshot{
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

func (s *rendererSnapshot) paneVisible(paneID uint32) bool {
	if paneID == 0 {
		return false
	}
	_, ok := s.visiblePaneIDs[paneID]
	return ok
}

func (s *rendererSnapshot) paneInActiveLayout(paneID uint32) bool {
	return s.layout != nil && s.layout.FindByPaneID(paneID) != nil
}

func (s *rendererSnapshot) visiblePaneSet(layoutHeight int) map[uint32]struct{} {
	root := s.visibleLayout(layoutHeight)
	if root == nil {
		return nil
	}
	visible := make(map[uint32]struct{})
	root.Walk(func(cell *mux.LayoutCell) {
		if paneID := cell.CellPaneID(); paneID != 0 {
			visible[paneID] = struct{}{}
		}
	})
	return visible
}

func (s *rendererSnapshot) paneDimensions(paneID uint32) (int, int, bool) {
	if s.layout != nil {
		if cell := s.layout.FindByPaneID(paneID); cell != nil {
			return cell.W, mux.PaneContentHeight(cell.H), true
		}
	}
	for _, ws := range s.windows {
		if cell := proto.FindCellInSnapshot(ws.Root, paneID); cell != nil {
			return cell.W, mux.PaneContentHeight(cell.H), true
		}
	}
	layoutHeight := s.height - render.GlobalBarHeight
	if s.width <= 0 || layoutHeight <= 0 {
		return 0, 0, false
	}
	return s.width, mux.PaneContentHeight(layoutHeight), true
}

func cloneWindowSnapshots(src []proto.WindowSnapshot) []proto.WindowSnapshot {
	return append([]proto.WindowSnapshot(nil), src...)
}

type rendererCommand struct {
	run  func(*rendererActorState)
	done chan struct{}
}

type rendererActorState struct {
	snapshot          *rendererSnapshot
	emulators         map[uint32]mux.TerminalEmulator
	pendingPaneOutput map[uint32]*paneOutputBuffer
	compositor        *render.Compositor
}

// paneOutputBuffer keeps raw PTY bytes for hidden panes until a visible or
// capture path needs emulator state. This intentionally trades CPU for memory;
// hidden-pane buffers are uncapped for now and can grow with hidden output.
// replayWidth/replayHeight capture the pane size the hidden shell was still
// using when buffering started so a cold emulator can replay at the source
// width before the normal visible-pane resize path catches up.
type paneOutputBuffer struct {
	chunks       [][]byte
	replayWidth  int
	replayHeight int
}

func (b *paneOutputBuffer) appendChunk(data []byte) {
	if len(data) == 0 {
		return
	}
	b.chunks = append(b.chunks, append([]byte(nil), data...))
}

func (b *paneOutputBuffer) empty() bool {
	return len(b.chunks) == 0
}

func (b *paneOutputBuffer) setReplayDimensions(width, height int) {
	if width <= 0 || height <= 0 || (b.replayWidth > 0 && b.replayHeight > 0) {
		return
	}
	b.replayWidth = width
	b.replayHeight = height
}

func (b *paneOutputBuffer) replayDimensions() (int, int, bool) {
	if b.replayWidth <= 0 || b.replayHeight <= 0 {
		return 0, 0, false
	}
	return b.replayWidth, b.replayHeight, true
}

func (b *paneOutputBuffer) flush(emu mux.TerminalEmulator) {
	for _, chunk := range b.chunks {
		_, _ = emu.Write(chunk)
	}
	b.chunks = nil
}

func (st *rendererActorState) bufferPaneOutput(paneID uint32, data []byte) {
	if paneID == 0 || len(data) == 0 {
		return
	}
	buf := st.pendingPaneOutput[paneID]
	if buf == nil {
		buf = &paneOutputBuffer{}
		st.pendingPaneOutput[paneID] = buf
	}
	if width, height, ok := st.snapshot.paneDimensions(paneID); ok {
		buf.setReplayDimensions(width, height)
	}
	buf.appendChunk(data)
}

// paneEmulatorDimensions returns the size a cold emulator should use before any
// buffered replay runs. Hidden panes prefer the buffered source dimensions so
// VT replay matches the shell's original cursor math; once visible, the normal
// resize path updates the emulator to the current layout dimensions.
func (st *rendererActorState) paneEmulatorDimensions(snap *rendererSnapshot, paneID uint32) (int, int, bool) {
	if buf := st.pendingPaneOutput[paneID]; buf != nil {
		if width, height, ok := buf.replayDimensions(); ok {
			return width, height, true
		}
	}
	return snap.paneDimensions(paneID)
}

func (st *rendererActorState) ensurePaneEmulator(paneID uint32) mux.TerminalEmulator {
	if paneID == 0 {
		return nil
	}
	if emu := st.emulators[paneID]; emu != nil {
		return emu
	}
	if _, ok := st.snapshot.paneInfo[paneID]; !ok {
		return nil
	}
	width, height, ok := st.paneEmulatorDimensions(st.snapshot, paneID)
	if !ok {
		return nil
	}
	emu := mux.NewVTEmulatorWithDrainAndScrollback(width, height, st.snapshot.scrollbackLines)
	st.emulators[paneID] = emu
	return emu
}

func (st *rendererActorState) warmPaneOutput(paneID uint32, emulators map[uint32]mux.TerminalEmulator) {
	if paneID == 0 {
		return
	}
	buf := st.pendingPaneOutput[paneID]
	if buf == nil || buf.empty() {
		return
	}
	emu := emulators[paneID]
	if emu == nil {
		delete(st.pendingPaneOutput, paneID)
		return
	}
	buf.flush(emu)
	delete(st.pendingPaneOutput, paneID)
}

func (st *rendererActorState) warmVisiblePanes(snap *rendererSnapshot, emulators map[uint32]mux.TerminalEmulator) {
	for paneID := range snap.visiblePaneIDs {
		st.warmPaneOutput(paneID, emulators)
	}
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
		snapshot:          initial,
		emulators:         make(map[uint32]mux.TerminalEmulator),
		pendingPaneOutput: make(map[uint32]*paneOutputBuffer),
		compositor:        render.NewCompositor(width, height, ""),
	}
	for cmd := range r.commands {
		cmd.run(state)
		close(cmd.done)
	}
}

func withRendererActorValue[T any](r *Renderer, run func(*rendererActorState) T) T {
	done := make(chan struct{})
	value := *new(T)
	r.commands <- rendererCommand{
		run: func(st *rendererActorState) {
			value = run(st)
		},
		done: done,
	}
	<-done
	return value
}
