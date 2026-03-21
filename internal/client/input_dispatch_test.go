package client

import (
	"net"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/copymode"
	"github.com/weill-labs/amux/internal/mouse"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func stubCopyToClipboard(t *testing.T, fn func(string)) {
	t.Helper()
	prevCopyToClipboard := copyToClipboard
	copyToClipboard = fn
	t.Cleanup(func() {
		copyToClipboard = prevCopyToClipboard
	})
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

func TestHandleMouseEventClickSendsFocusCommand(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	var drag DragState
	done := make(chan struct{})
	go func() {
		HandleMouseEvent(mouse.Event{
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

func TestHandleMouseEventCopyModeDragCopiesSelectionAndExits(t *testing.T) {
	cr := buildTestRenderer(t)
	cr.EnterCopyMode(1)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	var drag DragState

	var copied string
	stubCopyToClipboard(t, func(text string) {
		copied = text
	})

	y := mux.StatusLineRows
	HandleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      0,
		Y:      y,
	}, cr, sender, &drag)
	HandleMouseEvent(mouse.Event{
		Action: mouse.Motion,
		Button: mouse.ButtonLeft,
		X:      4,
		Y:      y,
		LastX:  0,
		LastY:  y,
	}, cr, sender, &drag)
	HandleMouseEvent(mouse.Event{
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
	var drag DragState

	y := mux.StatusLineRows
	for i := 0; i < 2; i++ {
		HandleMouseEvent(mouse.Event{
			Action: mouse.Press,
			Button: mouse.ButtonLeft,
			X:      1,
			Y:      y,
		}, cr, sender, &drag)
		HandleMouseEvent(mouse.Event{
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
	var drag DragState

	var copied string
	stubCopyToClipboard(t, func(text string) {
		copied = text
	})

	y := mux.StatusLineRows
	for i := 0; i < 3; i++ {
		HandleMouseEvent(mouse.Event{
			Action: mouse.Press,
			Button: mouse.ButtonLeft,
			X:      1,
			Y:      y,
		}, cr, sender, &drag)
		HandleMouseEvent(mouse.Event{
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
	stubCopyToClipboard(t, func(text string) {
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
	stubCopyToClipboard(t, func(text string) {
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
