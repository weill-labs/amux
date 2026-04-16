package client

import (
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/proto"
)

var errAttachProtocol = errors.New("attach protocol error")

type attachTimedReader interface {
	ReadMsg() (*proto.Message, error)
	ReadMsgWithTimeout(time.Duration) (*proto.Message, bool, error)
}

type attachReadResult struct {
	msg *proto.Message
	err error
}

// attachMessageSource uses conn deadlines while they work, then permanently
// switches to a single-reader pump when the transport rejects deadlines (for
// example, SSH channels).
type attachMessageSource struct {
	conn   net.Conn
	reader *proto.Reader
	pump   *attachMessagePump
}

type attachMessagePump struct {
	results chan attachReadResult
}

func newAttachMessageSource(conn net.Conn, reader *proto.Reader) *attachMessageSource {
	return &attachMessageSource{
		conn:   conn,
		reader: reader,
	}
}

func (s *attachMessageSource) pumpReader() *attachMessagePump {
	if s.pump == nil {
		s.pump = newAttachMessagePump(s.reader)
	}
	return s.pump
}

func newAttachMessagePump(reader *proto.Reader) *attachMessagePump {
	pump := &attachMessagePump{
		results: make(chan attachReadResult, 16),
	}
	go func() {
		defer close(pump.results)
		for {
			msg, err := reader.ReadMsg()
			pump.results <- attachReadResult{msg: msg, err: err}
			if err != nil {
				return
			}
		}
	}()
	return pump
}

func (p *attachMessagePump) ReadMsg() (*proto.Message, error) {
	result, ok := <-p.results
	if !ok {
		return nil, io.EOF
	}
	return result.msg, result.err
}

func (p *attachMessagePump) ReadMsgWithTimeout(timeout time.Duration) (*proto.Message, bool, error) {
	if timeout <= 0 {
		return nil, true, nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case result, ok := <-p.results:
		if !ok {
			return nil, false, io.EOF
		}
		return result.msg, false, result.err
	case <-timer.C:
		return nil, true, nil
	}
}

func (s *attachMessageSource) ReadMsg() (*proto.Message, error) {
	if s.pump != nil {
		return s.pump.ReadMsg()
	}
	return s.reader.ReadMsg()
}

func (s *attachMessageSource) ReadMsgWithTimeout(timeout time.Duration) (*proto.Message, bool, error) {
	if s.pump != nil {
		return s.pump.ReadMsgWithTimeout(timeout)
	}
	if err := s.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return s.pumpReader().ReadMsgWithTimeout(timeout)
	}
	defer s.conn.SetReadDeadline(time.Time{}) //nolint:errcheck // best-effort reset

	msg, err := s.reader.ReadMsg()
	if err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return nil, true, nil
		}
		return nil, false, err
	}
	return msg, false, nil
}

func readMsgWithTimeout(reader attachTimedReader, timeout time.Duration) (*proto.Message, bool, error) {
	return reader.ReadMsgWithTimeout(timeout)
}

func readMsgBeforeDeadline(reader attachTimedReader, deadline time.Time) (*proto.Message, bool, error) {
	return readMsgWithTimeout(reader, time.Until(deadline))
}

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

func releaseAttachBootstrapMessage(msg *proto.Message) {
	if msg == nil {
		return
	}
	msg.History = nil
	msg.StyledHistory = nil
	msg.PaneData = nil
	msg.Layout = nil
}

func applyAttachBootstrapReplayMessage(cr *ClientRenderer, msg attachBootstrapMessage) int {
	defer releaseAttachBootstrapMessage(msg.msg)
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
	return readImmediateAttachCorrectionFromSource(newAttachMessageSource(conn, reader), cr, timeout)
}

func readImmediateAttachCorrectionFromSource(reader attachTimedReader, cr *ClientRenderer, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		msg, timedOut, err := readMsgBeforeDeadline(reader, deadline)
		if timedOut {
			return nil
		}
		if err != nil {
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
	defer releaseAttachBootstrapMessage(msg.msg)
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
	return readAttachBootstrapPaneReplaysFromSource(newAttachMessageSource(conn, reader), cr, remainingOutputs, timeout)
}

func readAttachBootstrapPaneReplaysFromSource(reader attachTimedReader, cr *ClientRenderer, remainingOutputs int, timeout time.Duration) (int, error) {
	if remainingOutputs <= 0 {
		return 0, nil
	}
	deadline := time.Now().Add(timeout)
	for remainingOutputs > 0 {
		msg, timedOut, err := readMsgBeforeDeadline(reader, deadline)
		if timedOut {
			return remainingOutputs, nil
		}
		if err != nil {
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
	return readAttachBootstrapFromSource(newAttachMessageSource(conn, reader), cr)
}

func readAttachBootstrapFromSource(reader attachTimedReader, cr *ClientRenderer) error {
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

	remainingOutputs, err := readAttachBootstrapPaneReplaysFromSource(reader, cr, remainingOutputs, config.BootstrapPaneReplayWait)
	if err != nil {
		return err
	}
	if remainingOutputs > 0 {
		// A stuck pane replay should not keep the whole terminal black forever.
		// Any late pane-output or layout messages are still applied by the
		// normal message loop after the initial frame is rendered.
		return nil
	}

	return readImmediateAttachCorrectionFromSource(reader, cr, config.BootstrapCorrectionWindow)
}
