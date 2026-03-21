package server

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func TestHandleAttachFlushesQueuedPaneOutputAfterBootstrap(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newAttachTestPane(sess, 1, "pane-1", 80, 2)

	w := mux.NewWindow(pane, 80, 3)
	w.ID = 1
	w.Name = "window-1"
	if err := setAttachTestLayout(sess, []*mux.Window{w}, w.ID, []*mux.Pane{pane}); err != nil {
		t.Fatalf("setAttachTestLayout: %v", err)
	}
	pane.FeedOutput([]byte("line-1\r\nline-2\r\nline-3"))

	clientConn, replay, paused, release, done := startPausedAttach(t, srv, sess, 80, 4)
	defer closeAttach(t, clientConn, release, done)

	readInitialAttachReplay(t, clientConn, replay)
	waitForPause(t, paused)

	outputCh, unsubscribe := subscribeAttachTestPaneOutput(sess, pane.ID)
	defer unsubscribe()

	pane.FeedOutput([]byte("\r\nline-4\r\nline-5"))
	waitForSignal(t, outputCh, "queued pane output")

	want := pane.CaptureSnapshot()

	release()
	drainAttachMessages(t, clientConn, replay, 1)

	assertAttachReplayPaneMatchesSnapshot(t, replay, pane.ID, want)
}

func TestHandleAttachAppliesQueuedLayoutAfterConcurrentSplit(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane1 := newAttachTestPane(sess, 1, "pane-1", 80, 2)

	w := mux.NewWindow(pane1, 80, 3)
	w.ID = 1
	w.Name = "window-1"
	if err := setAttachTestLayout(sess, []*mux.Window{w}, w.ID, []*mux.Pane{pane1}); err != nil {
		t.Fatalf("setAttachTestLayout: %v", err)
	}
	pane1.FeedOutput([]byte("before-1\r\nbefore-2\r\nbefore-3"))

	clientConn, replay, paused, release, done := startPausedAttach(t, srv, sess, 80, 4)
	defer closeAttach(t, clientConn, release, done)

	readInitialAttachReplay(t, clientConn, replay)
	waitForPause(t, paused)

	pane2 := newAttachTestPane(sess, 2, "pane-2", 80, 2)
	if err := queueAttachTestSplit(sess, pane2, mux.SplitVertical, false); err != nil {
		t.Fatalf("queueAttachTestSplit: %v", err)
	}

	pane1Ch, unsubscribePane1 := subscribeAttachTestPaneOutput(sess, pane1.ID)
	defer unsubscribePane1()
	pane2Ch, unsubscribePane2 := subscribeAttachTestPaneOutput(sess, pane2.ID)
	defer unsubscribePane2()

	pane1.FeedOutput([]byte("\r\nsplit-pane-1"))
	pane2.FeedOutput([]byte("split-pane-2"))
	waitForSignal(t, pane1Ch, "pane-1 queued output")
	waitForSignal(t, pane2Ch, "pane-2 queued output")

	wantLayout, err := snapshotAttachTestLayout(sess)
	if err != nil {
		t.Fatalf("snapshotAttachTestLayout: %v", err)
	}
	wantPane1 := pane1.CaptureSnapshot()
	wantPane2 := pane2.CaptureSnapshot()

	release()
	drainAttachMessages(t, clientConn, replay, 2)

	assertAttachReplayLayoutMatchesSnapshot(t, replay, wantLayout)
	assertAttachReplayPaneMatchesSnapshot(t, replay, pane1.ID, wantPane1)
	assertAttachReplayPaneMatchesSnapshot(t, replay, pane2.ID, wantPane2)
}

func startPausedAttach(t *testing.T, srv *Server, sess *Session, cols, rows int) (net.Conn, *attachReplayState, <-chan struct{}, func(), <-chan struct{}) {
	t.Helper()

	serverConn, clientConn := net.Pipe()
	paused := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	done := make(chan struct{})

	srv.attachBootstrapHook = func() {
		close(paused)
		<-release
	}

	go func() {
		defer close(done)
		srv.handleAttach(serverConn, &Message{
			Type:    MsgTypeAttach,
			Session: sess.Name,
			Cols:    cols,
			Rows:    rows,
		})
	}()

	return clientConn, newAttachReplayState(), paused, func() {
		releaseOnce.Do(func() { close(release) })
	}, done
}

func closeAttach(t *testing.T, clientConn net.Conn, release func(), done <-chan struct{}) {
	t.Helper()

	release()
	if clientConn != nil {
		_ = WriteMsg(clientConn, &Message{Type: MsgTypeDetach})
		_ = clientConn.Close()
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleAttach did not exit")
	}
}

func readInitialAttachReplay(t *testing.T, conn net.Conn, replay *attachReplayState) *proto.LayoutSnapshot {
	t.Helper()

	var layout *proto.LayoutSnapshot
	for layout == nil {
		msg := readMsgWithTimeout(t, conn)
		if msg.Type != MsgTypeLayout {
			continue
		}
		layout = msg.Layout
		replay.HandleLayout(layout)
	}

	replayed := 0
	for replayed < len(layout.Panes) {
		msg := readMsgWithTimeout(t, conn)
		switch msg.Type {
		case MsgTypePaneHistory:
			replay.HandlePaneHistory(msg.PaneID, msg.History)
		case MsgTypePaneOutput:
			replay.HandlePaneOutput(msg.PaneID, msg.PaneData)
			replayed++
		default:
			t.Fatalf("unexpected bootstrap message: %+v", msg)
		}
	}

	return layout
}

func drainAttachMessages(t *testing.T, conn net.Conn, replay *attachReplayState, wantLayouts int) {
	t.Helper()

	layouts := 0
	for layouts < wantLayouts {
		msg := readMsgWithTimeout(t, conn)
		switch msg.Type {
		case MsgTypeLayout:
			replay.HandleLayout(msg.Layout)
			layouts++
		case MsgTypePaneHistory:
			replay.HandlePaneHistory(msg.PaneID, msg.History)
		case MsgTypePaneOutput:
			replay.HandlePaneOutput(msg.PaneID, msg.PaneData)
		default:
			t.Fatalf("unexpected post-bootstrap message: %+v", msg)
		}
	}
}

func assertAttachReplayLayoutMatchesSnapshot(t *testing.T, replay *attachReplayState, want *proto.LayoutSnapshot) {
	t.Helper()

	if replay.layout == nil {
		t.Fatal("missing replayed layout")
	}

	got := replay.layout
	if got, wantCount := len(got.Panes), len(want.Panes); got != wantCount {
		t.Fatalf("layout pane count = %d, want %d", got, wantCount)
	}

	for _, pane := range want.Panes {
		gotCell := proto.FindCellInSnapshot(got.Root, pane.ID)
		if gotCell == nil {
			t.Fatalf("pane %d missing from replayed layout", pane.ID)
		}
		wantCell := proto.FindCellInSnapshot(want.Root, pane.ID)
		if wantCell == nil {
			t.Fatalf("pane %d missing from expected layout", pane.ID)
		}
		if got, wantPos := gotCell.X, wantCell.X; got != wantPos {
			t.Fatalf("pane %d x = %d, want %d", pane.ID, got, wantPos)
		}
		if got, wantPos := gotCell.Y, wantCell.Y; got != wantPos {
			t.Fatalf("pane %d y = %d, want %d", pane.ID, got, wantPos)
		}
		if got, wantPos := gotCell.W, wantCell.W; got != wantPos {
			t.Fatalf("pane %d width = %d, want %d", pane.ID, got, wantPos)
		}
		if got, wantPos := gotCell.H, wantCell.H; got != wantPos {
			t.Fatalf("pane %d height = %d, want %d", pane.ID, got, wantPos)
		}
	}
}

func assertAttachReplayPaneMatchesSnapshot(t *testing.T, replay *attachReplayState, paneID uint32, want mux.CaptureSnapshot) {
	t.Helper()

	emu := replay.emulators[paneID]
	if emu == nil {
		t.Fatalf("missing replay emulator for pane %d", paneID)
	}

	content := mux.EmulatorContentLines(emu)
	if len(content) != len(want.Content) {
		t.Fatalf("pane %d content rows = %d, want %d", paneID, len(content), len(want.Content))
	}
	for i, wantLine := range want.Content {
		if got := content[i]; got != wantLine {
			t.Fatalf("pane %d content[%d] = %q, want %q", paneID, i, got, wantLine)
		}
	}

	col, row := emu.CursorPosition()
	hidden := emu.CursorHidden()
	if col != want.CursorCol || row != want.CursorRow || hidden != want.CursorHidden {
		t.Fatalf("pane %d cursor = col=%d row=%d hidden=%v, want col=%d row=%d hidden=%v", paneID, col, row, hidden, want.CursorCol, want.CursorRow, want.CursorHidden)
	}

	lines := replay.lines(paneID)
	wantLines := append([]string(nil), want.History...)
	wantLines = append(wantLines, want.Content...)
	if got, wantTotal := len(lines), len(wantLines); got != wantTotal {
		t.Fatalf("pane %d total lines = %d, want %d", paneID, got, wantTotal)
	}
	for i, wantLine := range wantLines {
		if got := lines[i]; got != wantLine {
			t.Fatalf("pane %d line %d = %q, want %q", paneID, i, got, wantLine)
		}
	}
}

func waitForPause(t *testing.T, paused <-chan struct{}) {
	t.Helper()
	waitForSignal(t, paused, "bootstrap pause")
}

func waitForSignal(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	if ch == nil {
		t.Fatalf("missing channel for %s", label)
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("timeout waiting for %s", label)
	}
}

func newAttachTestPane(sess *Session, id uint32, name string, cols, rows int) *mux.Pane {
	return newProxyPane(id, mux.PaneMeta{
		Name:  name,
		Host:  mux.DefaultHost,
		Color: config.CatppuccinMocha[(id-1)%uint32(len(config.CatppuccinMocha))],
	}, cols, rows, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})
}

func setAttachTestLayout(sess *Session, windows []*mux.Window, activeWindowID uint32, panes []*mux.Pane) error {
	_, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
		sess.Windows = windows
		sess.ActiveWindowID = activeWindowID
		sess.Panes = panes
		return struct{}{}, nil
	})
	return err
}

func subscribeAttachTestPaneOutput(sess *Session, paneID uint32) (chan struct{}, func()) {
	ch := sess.enqueuePaneOutputSubscribe(paneID)
	cleanup := func() {}
	if ch != nil {
		cleanup = func() {
			sess.enqueuePaneOutputUnsubscribe(paneID, ch)
		}
	}
	return ch, cleanup
}

func queueAttachTestSplit(sess *Session, pane *mux.Pane, dir mux.SplitDir, rootLevel bool) error {
	res := sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		if err := sess.insertPreparedPaneIntoActiveWindow(pane, dir, rootLevel); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{broadcastLayout: true}
	})
	if res.err != nil {
		return res.err
	}
	if res.broadcastLayout {
		sess.broadcastLayout()
	}
	return nil
}

func snapshotAttachTestLayout(sess *Session) (*proto.LayoutSnapshot, error) {
	return enqueueSessionQuery(sess, func(sess *Session) (*proto.LayoutSnapshot, error) {
		idleSnap := sess.snapshotIdleState()
		return sess.snapshotLayout(idleSnap), nil
	})
}

type attachReplayState struct {
	layout    *proto.LayoutSnapshot
	emulators map[uint32]mux.TerminalEmulator
	histories map[uint32][]string
}

func newAttachReplayState() *attachReplayState {
	return &attachReplayState{
		emulators: make(map[uint32]mux.TerminalEmulator),
		histories: make(map[uint32][]string),
	}
}

func (r *attachReplayState) HandleLayout(layout *proto.LayoutSnapshot) {
	r.layout = layout

	allPanes := layout.Panes
	activeRoot := layout.Root
	if len(layout.Windows) > 0 {
		allPanes = nil
		for _, ws := range layout.Windows {
			allPanes = append(allPanes, ws.Panes...)
			if ws.ID == layout.ActiveWindowID {
				activeRoot = ws.Root
			}
		}
	}

	paneIDs := make(map[uint32]struct{}, len(allPanes))
	for _, pane := range allPanes {
		paneIDs[pane.ID] = struct{}{}
		w, h := proto.FindPaneDimensions(layout, activeRoot, pane.ID, mux.PaneContentHeight)
		if emu, ok := r.emulators[pane.ID]; ok {
			emu.Resize(w, h)
			continue
		}
		r.emulators[pane.ID] = mux.NewVTEmulatorWithDrainAndScrollback(w, h, mux.DefaultScrollbackLines)
	}

	for paneID := range r.emulators {
		if _, ok := paneIDs[paneID]; ok {
			continue
		}
		delete(r.emulators, paneID)
		delete(r.histories, paneID)
	}
}

func (r *attachReplayState) HandlePaneHistory(paneID uint32, history []string) {
	r.histories[paneID] = append([]string(nil), history...)
}

func (r *attachReplayState) HandlePaneOutput(paneID uint32, data []byte) {
	emu := r.emulators[paneID]
	if emu == nil {
		panic("pane output before layout for attach replay")
	}
	if _, err := emu.Write(data); err != nil {
		panic(err)
	}
}

func (r *attachReplayState) lines(paneID uint32) []string {
	lines := append([]string(nil), r.histories[paneID]...)
	lines = append(lines, mux.EmulatorScrollbackLines(r.emulators[paneID])...)
	lines = append(lines, mux.EmulatorContentLines(r.emulators[paneID])...)
	return lines
}
