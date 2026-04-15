package client

import (
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/proto"
)

var errAttachProtocol = errors.New("attach protocol error")

type attachBootstrapMessage struct {
	msg *proto.Message
}

func newAttachBootstrapMessage(msg *proto.Message) (attachBootstrapMessage, bool) {
	switch msg.Type {
	case proto.MsgTypePaneHistory:
		return attachBootstrapMessage{msg: msg}, true
	case proto.MsgTypePaneOutput:
		return attachBootstrapMessage{msg: msg}, true
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

func applyAttachBootstrapReplayMessage(cr *ClientRenderer, msg attachBootstrapMessage) int {
	switch msg.msg.Type {
	case proto.MsgTypePaneHistory:
		cr.AppendPaneHistoryMessage(msg.msg.PaneID, msg.msg.History, msg.msg.StyledHistory)
		return 0
	case proto.MsgTypePaneOutput:
		cr.HandlePaneOutput(msg.msg.PaneID, msg.msg.PaneData)
		return 1
	default:
		return 0
	}
}

func readImmediateAttachCorrection(conn net.Conn, reader *proto.Reader, cr *ClientRenderer, timeout time.Duration) error {
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	defer conn.SetReadDeadline(time.Time{}) //nolint:errcheck // best-effort reset
	for {
		msg, err := reader.ReadMsg()
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

func applyAttachBootstrapMessage(cr *ClientRenderer, msg attachBootstrapMessage) int {
	switch msg.msg.Type {
	case proto.MsgTypePaneHistory:
		cr.HandlePaneHistoryMessage(msg.msg.PaneID, msg.msg.History, msg.msg.StyledHistory)
		return 0
	case proto.MsgTypePaneOutput:
		cr.HandlePaneOutput(msg.msg.PaneID, msg.msg.PaneData)
		return 1
	default:
		return 0
	}
}

func readAttachBootstrapPaneReplays(conn net.Conn, reader *proto.Reader, cr *ClientRenderer, remainingOutputs int, timeout time.Duration) (int, error) {
	if remainingOutputs <= 0 {
		return 0, nil
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return remainingOutputs, err
	}
	defer conn.SetReadDeadline(time.Time{}) //nolint:errcheck // best-effort reset
	for remainingOutputs > 0 {
		msg, err := reader.ReadMsg()
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
		remainingOutputs -= applyAttachBootstrapReplayMessage(cr, bufferedMsg)
	}
	return 0, nil
}

func readAttachBootstrap(conn net.Conn, reader *proto.Reader, cr *ClientRenderer) error {
	var layout *proto.LayoutSnapshot
	var buffered []attachBootstrapMessage

	for layout == nil {
		msg, err := reader.ReadMsg()
		if err != nil {
			return err
		}
		switch msg.Type {
		case proto.MsgTypeLayout:
			layout = msg.Layout
		default:
			bufferedMsg, ok := newAttachBootstrapMessage(msg)
			if !ok {
				return fmt.Errorf("%w: unexpected attach bootstrap message type %d before layout", errAttachProtocol, msg.Type)
			}
			buffered = append(buffered, bufferedMsg)
		}
	}

	cr.HandleLayout(layout)

	remainingOutputs := attachBootstrapPaneCount(layout)
	for _, msg := range buffered {
		remainingOutputs -= applyAttachBootstrapReplayMessage(cr, msg)
	}

	remainingOutputs, err := readAttachBootstrapPaneReplays(conn, reader, cr, remainingOutputs, config.BootstrapPaneReplayWait)
	if err != nil {
		return err
	}
	if remainingOutputs > 0 {
		// A stuck pane replay should not keep the whole terminal black forever.
		// Any late pane-output or layout messages are still applied by the
		// normal message loop after the initial frame is rendered.
		return nil
	}

	return readImmediateAttachCorrection(conn, reader, cr, config.BootstrapCorrectionWindow)
}
