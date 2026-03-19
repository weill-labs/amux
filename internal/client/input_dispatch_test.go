package client

import (
	"net"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mouse"
	"github.com/weill-labs/amux/internal/proto"
)

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
