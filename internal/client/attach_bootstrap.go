package client

import (
	"fmt"
	"net"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/proto"
)

type attachBootstrapMessage struct {
	typ     proto.MsgType
	paneID  uint32
	history []string
	data    []byte
}

func newAttachBootstrapMessage(msg *proto.Message) (attachBootstrapMessage, bool) {
	switch msg.Type {
	case proto.MsgTypePaneHistory:
		return attachBootstrapMessage{
			typ:     msg.Type,
			paneID:  msg.PaneID,
			history: append([]string(nil), msg.History...),
		}, true
	case proto.MsgTypePaneOutput:
		return attachBootstrapMessage{
			typ:    msg.Type,
			paneID: msg.PaneID,
			data:   append([]byte(nil), msg.PaneData...),
		}, true
	default:
		return attachBootstrapMessage{}, false
	}
}

func attachBootstrapPaneCount(layout *proto.LayoutSnapshot) int {
	if layout == nil {
		return 0
	}
	if len(layout.Windows) == 0 {
		return len(layout.Panes)
	}
	count := 0
	for _, ws := range layout.Windows {
		count += len(ws.Panes)
	}
	return count
}

func applyAttachBootstrapMessage(cr *ClientRenderer, msg attachBootstrapMessage) int {
	switch msg.typ {
	case proto.MsgTypePaneHistory:
		cr.HandlePaneHistory(msg.paneID, msg.history)
		return 0
	case proto.MsgTypePaneOutput:
		cr.HandlePaneOutput(msg.paneID, msg.data)
		return 1
	default:
		return 0
	}
}

func readImmediateAttachCorrection(conn net.Conn, cr *ClientRenderer, timeout time.Duration) error {
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	defer conn.SetReadDeadline(time.Time{}) //nolint:errcheck // best-effort reset
	for {
		msg, err := proto.ReadMsg(conn)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return nil
			}
			return err
		}
		if msg.Type == proto.MsgTypeLayout {
			cr.HandleLayout(msg.Layout)
			continue
		}
		bufferedMsg, ok := newAttachBootstrapMessage(msg)
		if !ok {
			// Unknown message types (bell, copy-mode, etc.) end the correction
			// window without failing attach. Later layout or pane-output updates
			// still flow through the normal message loop.
			return nil
		}
		applyAttachBootstrapMessage(cr, bufferedMsg)
	}
}

func readAttachBootstrapPaneReplays(conn net.Conn, cr *ClientRenderer, remainingOutputs int, timeout time.Duration) (int, error) {
	if remainingOutputs <= 0 {
		return 0, nil
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return remainingOutputs, err
	}
	defer conn.SetReadDeadline(time.Time{}) //nolint:errcheck // best-effort reset
	for remainingOutputs > 0 {
		msg, err := proto.ReadMsg(conn)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return remainingOutputs, nil
			}
			return remainingOutputs, err
		}
		if msg.Type == proto.MsgTypeLayout {
			cr.HandleLayout(msg.Layout)
			continue
		}
		bufferedMsg, ok := newAttachBootstrapMessage(msg)
		if !ok {
			// Unknown message types during pane replay should not fail attach.
			// Exit bootstrap early and let later state continue via the normal loop.
			return remainingOutputs, nil
		}
		remainingOutputs -= applyAttachBootstrapMessage(cr, bufferedMsg)
	}
	return 0, nil
}

func readAttachBootstrap(conn net.Conn, cr *ClientRenderer) error {
	var layout *proto.LayoutSnapshot
	var buffered []attachBootstrapMessage

	for layout == nil {
		msg, err := proto.ReadMsg(conn)
		if err != nil {
			return err
		}
		switch msg.Type {
		case proto.MsgTypeLayout:
			layout = msg.Layout
		default:
			bufferedMsg, ok := newAttachBootstrapMessage(msg)
			if !ok {
				return fmt.Errorf("unexpected attach bootstrap message type %d before layout", msg.Type)
			}
			buffered = append(buffered, bufferedMsg)
		}
	}

	cr.HandleLayout(layout)

	remainingOutputs := attachBootstrapPaneCount(layout)
	for _, msg := range buffered {
		remainingOutputs -= applyAttachBootstrapMessage(cr, msg)
	}

	remainingOutputs, err := readAttachBootstrapPaneReplays(conn, cr, remainingOutputs, config.BootstrapPaneReplayWait)
	if err != nil {
		return err
	}
	if remainingOutputs > 0 {
		// A stuck pane replay should not keep the whole terminal black forever.
		// Any late pane-output or layout messages are still applied by the
		// normal message loop after the initial frame is rendered.
		return nil
	}

	return readImmediateAttachCorrection(conn, cr, config.BootstrapCorrectionWindow)
}
