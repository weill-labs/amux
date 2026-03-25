package client

import (
	"bytes"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/copymode"
	"github.com/weill-labs/amux/internal/mouse"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func stubCopyToClipboard(cr *ClientRenderer, fn func(string)) {
	cr.CopyToClipboard = fn
}

func readCommandMessage(t *testing.T, conn net.Conn) *proto.Message {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	msg, err := proto.ReadMsg(conn)
	if err != nil {
		t.Fatalf("read command message: %v", err)
	}
	return msg
}

func buildMultiWindowRendererAt(t *testing.T, activeWindowID uint32) *ClientRenderer {
	t.Helper()

	snap := multiWindow80x23()
	snap.ActiveWindowID = activeWindowID
	for _, ws := range snap.Windows {
		if ws.ID == activeWindowID {
			snap.ActivePaneID = ws.ActivePaneID
			snap.Root = ws.Root
			break
		}
	}

	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(snap)
	return cr
}

func globalBarClickColumn(t *testing.T, cr *ClientRenderer, label string) int {
	t.Helper()

	lines := strings.Split(cr.Capture(true), "\n")
	if len(lines) == 0 {
		t.Fatal("capture should include a global bar")
	}
	bar := lines[len(lines)-1]
	col := strings.Index(bar, label)
	if col < 0 {
		t.Fatalf("global bar %q missing %q", bar, label)
	}
	return col
}

func globalBarRow(t *testing.T, cr *ClientRenderer) int {
	t.Helper()

	layout := cr.VisibleLayout()
	if layout == nil {
		t.Fatal("visible layout missing")
	}
	return layout.H
}

func TestHandleDisplayPaneSelectionSendsFocusCommand(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	if !cr.ShowDisplayPanes() {
		t.Fatal("ShowDisplayPanes should succeed")
	}

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	done := make(chan struct{})
	go func() {
		handleDisplayPaneSelection(cr, sender, '2')
		close(done)
	}()

	msg := readCommandMessage(t, serverConn)
	if msg.Type != proto.MsgTypeCommand {
		t.Fatalf("message type = %d, want %d", msg.Type, proto.MsgTypeCommand)
	}
	if msg.CmdName != "focus" {
		t.Fatalf("command = %q, want focus", msg.CmdName)
	}
	if len(msg.CmdArgs) != 1 || msg.CmdArgs[0] != "2" {
		t.Fatalf("command args = %v, want [2]", msg.CmdArgs)
	}
	<-done
	if cr.DisplayPanesActive() {
		t.Fatal("display-panes overlay should hide after selection")
	}
}

func TestSplitBindingArgsInjectsActivePane(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)

	got, ok := splitBindingArgs(cr, config.Binding{Action: "split-focus", Args: []string{"root", "v"}})
	if !ok {
		t.Fatal("splitBindingArgs should succeed when layout is ready")
	}
	want := []string{"pane-1", "root", "v"}
	if len(got) != len(want) {
		t.Fatalf("splitBindingArgs length = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitBindingArgs[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestSplitBindingArgsRejectsUnknownActivePane(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)

	if got, ok := splitBindingArgs(cr, config.Binding{Action: "split-focus", Args: []string{"v"}}); ok || got != nil {
		t.Fatalf("splitBindingArgs without layout = (%v, %t), want (nil, false)", got, ok)
	}
}

func TestHandleSplitBindingShowsErrorWhenLayoutNotReady(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	var rendered bytes.Buffer
	handleSplitBinding(cr, sender, config.Binding{Action: "split-focus", Args: []string{"v"}}, &rendered)

	if !strings.Contains(rendered.String(), "\a") {
		t.Fatalf("split binding error should ring bell, got %q", rendered.String())
	}
	if got := cr.prefixMessage(); got != "cannot split: layout not ready yet" {
		t.Fatalf("split binding feedback = %q, want %q", got, "cannot split: layout not ready yet")
	}
	if err := serverConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	if _, err := proto.ReadMsg(serverConn); err == nil {
		t.Fatal("split binding without layout should not send a command")
	} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatalf("read command message error = %v, want timeout", err)
	}
}

func TestHandleSplitBindingSendsCommandWhenLayoutReady(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	var rendered bytes.Buffer
	done := make(chan struct{})
	go func() {
		handleSplitBinding(cr, sender, config.Binding{Action: "split-focus", Args: []string{"root", "v"}}, &rendered)
		close(done)
	}()

	msg := readCommandMessage(t, serverConn)
	if msg.Type != proto.MsgTypeCommand {
		t.Fatalf("message type = %d, want %d", msg.Type, proto.MsgTypeCommand)
	}
	if msg.CmdName != "split-focus" {
		t.Fatalf("command = %q, want split-focus", msg.CmdName)
	}
	if want := []string{"pane-1", "root", "v"}; len(msg.CmdArgs) != len(want) {
		t.Fatalf("command args length = %v, want %v", msg.CmdArgs, want)
	} else {
		for i := range want {
			if msg.CmdArgs[i] != want[i] {
				t.Fatalf("command args[%d] = %q, want %q (full=%v)", i, msg.CmdArgs[i], want[i], msg.CmdArgs)
			}
		}
	}
	<-done
	if rendered.Len() != 0 {
		t.Fatalf("successful split binding should not render immediate feedback, got %q", rendered.String())
	}
}

func TestHandleMouseEventClickSendsFocusCommand(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	var drag dragState
	done := make(chan struct{})
	go func() {
		handleMouseEvent(mouse.Event{
			Action: mouse.Press,
			Button: mouse.ButtonLeft,
			X:      60,
			Y:      5,
		}, cr, sender, &drag)
		close(done)
	}()

	msg := readCommandMessage(t, serverConn)
	if msg.Type != proto.MsgTypeCommand {
		t.Fatalf("message type = %d, want %d", msg.Type, proto.MsgTypeCommand)
	}
	if msg.CmdName != "focus" {
		t.Fatalf("command = %q, want focus", msg.CmdName)
	}
	if len(msg.CmdArgs) != 1 || msg.CmdArgs[0] != "2" {
		t.Fatalf("command args = %v, want [2]", msg.CmdArgs)
	}
	<-done
}

func TestHandleMouseEventGlobalBarClickSendsSelectWindowCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		activeWindow uint32
		clickLabel   string
		wantWindow   string
	}{
		{
			name:         "click second tab from first window",
			activeWindow: 1,
			clickLabel:   "2:logs",
			wantWindow:   "2",
		},
		{
			name:         "click first tab from second window",
			activeWindow: 2,
			clickLabel:   "1:editor",
			wantWindow:   "1",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cr := buildMultiWindowRendererAt(t, tt.activeWindow)

			clientConn, serverConn := net.Pipe()
			t.Cleanup(func() {
				clientConn.Close()
				serverConn.Close()
			})

			sender := newMessageSender(clientConn)
			t.Cleanup(sender.Close)

			var drag dragState
			x := globalBarClickColumn(t, cr, tt.clickLabel)
			y := globalBarRow(t, cr)

			done := make(chan struct{})
			go func() {
				handleMouseEvent(mouse.Event{
					Action: mouse.Press,
					Button: mouse.ButtonLeft,
					X:      x,
					Y:      y,
				}, cr, sender, &drag)
				close(done)
			}()

			msg := readCommandMessage(t, serverConn)
			if msg.Type != proto.MsgTypeCommand {
				t.Fatalf("message type = %d, want %d", msg.Type, proto.MsgTypeCommand)
			}
			if msg.CmdName != "select-window" {
				t.Fatalf("command = %q, want select-window", msg.CmdName)
			}
			if len(msg.CmdArgs) != 1 || msg.CmdArgs[0] != tt.wantWindow {
				t.Fatalf("command args = %v, want [%s]", msg.CmdArgs, tt.wantWindow)
			}
			<-done
		})
	}
}

func TestHandleMouseEventGlobalBarClickOutsideTabsDoesNothing(t *testing.T) {
	t.Parallel()

	cr := buildMultiWindowRenderer(t)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	var drag dragState
	x := globalBarClickColumn(t, cr, "panes")
	y := globalBarRow(t, cr)

	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      x,
		Y:      y,
	}, cr, sender, &drag)

	assertNoMessage(t, serverConn)
}

func TestHandleMouseEventGlobalBarClickSingleWindowDoesNothing(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	var drag dragState
	x := globalBarClickColumn(t, cr, "amux")
	y := globalBarRow(t, cr)

	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      x,
		Y:      y,
	}, cr, sender, &drag)

	assertNoMessage(t, serverConn)
}

func TestPaneAllowsMouseCopySelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(*ClientRenderer)
		pane  uint32
		want  bool
	}{
		{
			name: "missing pane",
			pane: 99,
			want: false,
		},
		{
			name: "regular pane",
			pane: 1,
			want: true,
		},
		{
			name: "alt screen pane",
			setup: func(cr *ClientRenderer) {
				cr.HandlePaneOutput(1, []byte("\x1b[?1049h"))
			},
			pane: 1,
			want: false,
		},
		{
			name: "app mouse pane",
			setup: func(cr *ClientRenderer) {
				cr.HandlePaneOutput(1, []byte("\x1b[?1002h\x1b[?1006h"))
			},
			pane: 1,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cr := buildTestRenderer(t)
			if tt.setup != nil {
				tt.setup(cr)
			}

			if got := paneAllowsMouseCopySelection(cr, tt.pane); got != tt.want {
				t.Fatalf("paneAllowsMouseCopySelection(%d) = %v, want %v", tt.pane, got, tt.want)
			}
		})
	}
}

func TestHandleMouseEventBorderPressClearsCopyDragState(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	layout := cr.VisibleLayout()
	if layout == nil {
		t.Fatal("visible layout missing")
	}

	borderX := -1
	for x := 0; x < 80; x++ {
		if layout.FindBorderAt(x, 5) != nil {
			borderX = x
			break
		}
	}
	if borderX < 0 {
		t.Fatal("expected a vertical border in the test layout")
	}

	var drag dragState
	drag.CopyModeActive = true
	drag.CopyModePaneID = 1
	drag.CopyMoved = true

	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      borderX,
		Y:      5,
	}, cr, nil, &drag)

	if !drag.Active {
		t.Fatal("border press should start a resize drag")
	}
	if drag.CopyModeActive {
		t.Fatal("border press should clear copy-mode drag state")
	}
	if drag.CopyModePaneID != 0 {
		t.Fatalf("border press should clear copy-mode pane id, got %d", drag.CopyModePaneID)
	}
}

func TestHandleMouseEventDragStartsCopyModeAndCopiesSelection(t *testing.T) {
	cr := buildTestRenderer(t)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	var drag dragState

	var copied string
	stubCopyToClipboard(cr, func(text string) {
		copied = text
	})

	y := mux.StatusLineRows
	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      0,
		Y:      y,
	}, cr, sender, &drag)

	if cr.InCopyMode(1) {
		t.Fatal("pane-1 should not enter copy mode until the drag moves")
	}

	handleMouseEvent(mouse.Event{
		Action: mouse.Motion,
		Button: mouse.ButtonLeft,
		X:      4,
		Y:      y,
		LastX:  0,
		LastY:  y,
	}, cr, sender, &drag)

	cm := cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("pane-1 should enter copy mode on mouse drag")
	}
	if got := cm.SelectedText(); got != "hello" {
		t.Fatalf("selected text during drag = %q, want %q", got, "hello")
	}

	handleMouseEvent(mouse.Event{
		Action: mouse.Release,
		Button: mouse.ButtonLeft,
		X:      4,
		Y:      y,
		LastX:  4,
		LastY:  y,
	}, cr, sender, &drag)

	if copied != "hello" {
		t.Fatalf("copied text = %q, want %q", copied, "hello")
	}
	if cr.InCopyMode(1) {
		t.Fatal("pane-1 should exit copy mode after mouse drag copy")
	}
}

func TestHandleMouseEventDragMotionWithMissingPaneDoesNotEnterCopyMode(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(&proto.LayoutSnapshot{
		SessionName:  "test",
		ActivePaneID: 99,
		Width:        80,
		Height:       23,
		Root: proto.CellSnapshot{
			X: 0, Y: 0, W: 80, H: 23,
			IsLeaf: true, Dir: -1, PaneID: 99,
		},
	})

	var drag dragState
	drag.CopyModePaneID = 99

	handleMouseEvent(mouse.Event{
		Action: mouse.Motion,
		Button: mouse.ButtonLeft,
		X:      1,
		Y:      mux.StatusLineRows,
	}, cr, nil, &drag)

	if cr.InCopyMode(99) {
		t.Fatal("missing pane should not enter copy mode on mouse drag")
	}
	if drag.CopyModeActive {
		t.Fatal("missing pane should leave copy-mode drag inactive")
	}
}

func TestHandleMouseEventCopyModeDragCopiesSelectionAndExits(t *testing.T) {
	cr := buildTestRenderer(t)
	cr.EnterCopyMode(1)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	var drag dragState

	var copied string
	stubCopyToClipboard(cr, func(text string) {
		copied = text
	})

	y := mux.StatusLineRows
	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      0,
		Y:      y,
	}, cr, sender, &drag)
	handleMouseEvent(mouse.Event{
		Action: mouse.Motion,
		Button: mouse.ButtonLeft,
		X:      4,
		Y:      y,
		LastX:  0,
		LastY:  y,
	}, cr, sender, &drag)
	handleMouseEvent(mouse.Event{
		Action: mouse.Release,
		Button: mouse.ButtonLeft,
		X:      4,
		Y:      y,
		LastX:  4,
		LastY:  y,
	}, cr, sender, &drag)

	if copied != "hello" {
		t.Fatalf("copied text = %q, want %q", copied, "hello")
	}
	if cr.InCopyMode(1) {
		t.Fatal("pane-1 should exit copy mode after mouse drag copy")
	}
}

func TestHandleMouseEventCopyModeDoubleClickSelectsWordAndArmsCopy(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.EnterCopyMode(1)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	var drag dragState

	y := mux.StatusLineRows
	for i := 0; i < 2; i++ {
		handleMouseEvent(mouse.Event{
			Action: mouse.Press,
			Button: mouse.ButtonLeft,
			X:      1,
			Y:      y,
		}, cr, sender, &drag)
		handleMouseEvent(mouse.Event{
			Action: mouse.Release,
			Button: mouse.ButtonLeft,
			X:      1,
			Y:      y,
		}, cr, sender, &drag)
	}

	cm := cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("pane-1 should remain in copy mode until delayed word copy fires")
	}
	if got := cm.SelectedText(); got != "hello" {
		t.Fatalf("double click selected %q, want %q", got, "hello")
	}
	if drag.PendingWordCopyPaneID != 1 {
		t.Fatalf("pending word copy pane = %d, want 1", drag.PendingWordCopyPaneID)
	}
}

func TestHandleMouseEventCopyModeTripleClickCopiesLine(t *testing.T) {
	cr := buildTestRenderer(t)
	cr.EnterCopyMode(1)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	var drag dragState

	var copied string
	stubCopyToClipboard(cr, func(text string) {
		copied = text
	})

	y := mux.StatusLineRows
	for i := 0; i < 3; i++ {
		handleMouseEvent(mouse.Event{
			Action: mouse.Press,
			Button: mouse.ButtonLeft,
			X:      1,
			Y:      y,
		}, cr, sender, &drag)
		handleMouseEvent(mouse.Event{
			Action: mouse.Release,
			Button: mouse.ButtonLeft,
			X:      1,
			Y:      y,
		}, cr, sender, &drag)
	}

	if copied != "hello from pane 1\n" {
		t.Fatalf("triple click copied %q, want %q", copied, "hello from pane 1\n")
	}
	if cr.InCopyMode(1) {
		t.Fatal("pane-1 should exit copy mode after triple-click line copy")
	}
}

func TestCopyModeHelpersSetCursorStartSelectionAndCopy(t *testing.T) {
	cr := buildTestRenderer(t)
	cr.EnterCopyMode(1)

	if action := cr.CopyModeSetCursor(1, 1, 0); action != copymode.ActionRedraw {
		t.Fatalf("CopyModeSetCursor() = %d, want ActionRedraw", action)
	}
	if !cr.IsDirty() {
		t.Fatal("CopyModeSetCursor should mark the renderer dirty")
	}

	if action := cr.CopyModeStartSelection(1); action != copymode.ActionRedraw {
		t.Fatalf("CopyModeStartSelection() = %d, want ActionRedraw", action)
	}

	cm := cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("pane-1 copy mode missing")
	}
	if action := cm.HandleInput([]byte{'l', 'y'}); action != copymode.ActionYank {
		t.Fatalf("selection yank = %d, want ActionYank", action)
	}

	var copied string
	stubCopyToClipboard(cr, func(text string) {
		copied = text
	})

	cr.CopyModeCopySelection(1)
	if copied != "el" {
		t.Fatalf("copied text = %q, want %q", copied, "el")
	}
	if cr.InCopyMode(1) {
		t.Fatal("pane-1 should exit copy mode after copy")
	}
}

func TestCopyModeCopySelectionAppendsCopyBuffer(t *testing.T) {
	cr := buildTestRenderer(t)

	var copied []string
	stubCopyToClipboard(cr, func(text string) {
		copied = append(copied, text)
	})

	cr.EnterCopyMode(1)
	cm := cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("pane-1 copy mode missing")
	}
	cr.CopyModeSetCursor(1, 0, 0)
	cr.CopyModeStartSelection(1)
	if action := cm.HandleInput([]byte{'l', 'y'}); action != copymode.ActionYank {
		t.Fatalf("initial yank = %d, want ActionYank", action)
	}
	cr.CopyModeCopySelection(1)

	cr.EnterCopyMode(1)
	cm = cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("pane-1 copy mode missing after re-enter")
	}
	cr.CopyModeSetCursor(1, 2, 0)
	cr.CopyModeStartSelection(1)
	if action := cm.HandleInput([]byte{'l', 'A'}); action != copymode.ActionYank {
		t.Fatalf("append yank = %d, want ActionYank", action)
	}
	cr.CopyModeCopySelection(1)

	if len(copied) != 2 {
		t.Fatalf("clipboard writes = %d, want 2", len(copied))
	}
	if copied[0] != "he" {
		t.Fatalf("first clipboard write = %q, want %q", copied[0], "he")
	}
	if copied[1] != "hell" {
		t.Fatalf("second clipboard write = %q, want %q", copied[1], "hell")
	}
	if got := cr.CopyBuffer(); got != "hell" {
		t.Fatalf("copyBuffer = %q, want %q", got, "hell")
	}
}
