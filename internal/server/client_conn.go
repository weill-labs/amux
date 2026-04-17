package server

import (
	"errors"
	"fmt"
	"io"
	"net"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	charmlog "github.com/charmbracelet/log"
	"github.com/weill-labs/amux/internal/auditlog"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/termprofile"
)

const (
	disconnectReasonClientDetach = "client detach"
	disconnectReasonClosed       = "connection closed"
	disconnectReasonShutdown     = "server shutdown"
)

// clientConn manages a single client connection to the server.
type clientConn struct {
	conn               net.Conn
	reader             *proto.Reader
	ID                 string
	displayPanesShown  bool
	prefixMessageShown bool
	chooserMode        string
	copyModeShown      bool
	inputIdle          bool
	uiGeneration       uint64
	cols               int // last reported terminal width
	rows               int // last reported terminal height
	nonInteractive     bool
	writer             *clientWriter
	typeKeyQueue       *pacedInputQueue
	capabilities       proto.ClientCapabilities
	colorProfile       string
	predictions        map[uint32][]pendingPredictionEpoch
	logger             *charmlog.Logger
	bootstrapping      atomic.Bool
	disconnectReason   atomic.Pointer[string]
}

type pendingMessage struct {
	msg       *Message
	paneID    uint32
	outputSeq uint64
}

type pendingPredictionEpoch struct {
	epoch     uint32
	remaining []byte
}

// newClientConn wraps a net.Conn for protocol communication.
func newClientConn(conn net.Conn) *clientConn {
	cc := &clientConn{
		conn:        conn,
		reader:      proto.NewReader(conn),
		inputIdle:   true,
		predictions: make(map[uint32][]pendingPredictionEpoch),
		logger:      auditlog.Discard(),
	}
	cc.writer = newClientWriter(conn)
	return cc
}

func (cc *clientConn) setNegotiatedCapabilities(caps proto.ClientCapabilities) {
	cc.capabilities = caps
	cc.ensureWriter().setBinaryPaneHistory(caps.BinaryPaneHistory)
}

func (cc *clientConn) capabilitySummary() string {
	return cc.capabilities.Summary()
}

func (cc *clientConn) setColorProfile(name string) {
	if profile, ok := termprofile.Parse(name); ok {
		cc.colorProfile = termprofile.Format(profile)
		return
	}
	cc.colorProfile = ""
}

func (cc *clientConn) colorProfileValue() string {
	return cc.colorProfile
}

func (cc *clientConn) participatesInSizeNegotiation() bool {
	return cc != nil && !cc.nonInteractive
}

// Send enqueues a message to the client. Thread-safe.
func (cc *clientConn) Send(msg *Message) error {
	return cc.ensureWriter().send(msg)
}

func (cc *clientConn) Flush() error {
	cc.ensureWriter().flush()
	return nil
}

// Close shuts down the connection.
func (cc *clientConn) Close() {
	if cc.typeKeyQueue != nil {
		cc.typeKeyQueue.close()
	}
	cc.ensureWriter().close()
}

func (cc *clientConn) enqueueTypeKeys(chunks []encodedKeyChunk) error {
	queue := cc.typeKeyQueue
	if queue == nil {
		return errPacedInputClosed
	}
	return queue.enqueue(chunks)
}

func (cc *clientConn) enqueueTypeKeysToPane(paneID uint32, chunks []encodedKeyChunk) error {
	queue := cc.typeKeyQueue
	if queue == nil {
		return errPacedInputClosed
	}
	return queue.enqueueToPane(paneID, chunks)
}

func (cc *clientConn) initTypeKeyQueue() {
	if cc.typeKeyQueue != nil {
		return
	}
	cc.typeKeyQueue = newPacedInputQueue("client "+cc.ID, cc.logger, func(paneID uint32, data []byte) error {
		return cc.Send(&Message{Type: MsgTypeTypeKeys, PaneID: paneID, Input: data})
	})
}

func (cc *clientConn) startBootstrap() {
	cc.bootstrapping.Store(true)
	cc.ensureWriter().startBootstrap()
}

func (cc *clientConn) finishBootstrap(minOutputSeq map[uint32]uint64) {
	defer cc.bootstrapping.Store(false)
	cc.ensureWriter().finishBootstrap(minOutputSeq)
}

func (cc *clientConn) sendBroadcast(msg *Message) {
	cc.ensureWriter().sendBroadcast(msg)
}

func (cc *clientConn) sendBroadcastSync(msg *Message) {
	cc.ensureWriter().sendBroadcastSync(msg)
}

func (cc *clientConn) sendPaneOutput(msg *Message, paneID uint32, seq uint64) {
	cc.ensureWriter().sendPaneOutput(cc.decoratePaneOutput(msg, paneID), paneID, seq)
}

func (cc *clientConn) sendPaneMessage(msg *Message) {
	cc.ensureWriter().sendPaneMessage(msg)
}

func (cc *clientConn) isBootstrapping() bool {
	if cc == nil {
		return false
	}
	return cc.bootstrapping.Load()
}

func (cc *clientConn) ensureWriter() *clientWriter {
	return cc.writer
}

func (cc *clientConn) markDisconnectReason(reason string) {
	if cc == nil || reason == "" {
		return
	}
	reasonCopy := reason
	cc.disconnectReason.CompareAndSwap(nil, &reasonCopy)
}

func (cc *clientConn) disconnectReasonValue() string {
	if cc == nil {
		return ""
	}
	reason := cc.disconnectReason.Load()
	if reason == nil {
		return ""
	}
	return *reason
}

func (cc *clientConn) finalizeDisconnectReason(sess *Session, err error) {
	if err == nil || cc.disconnectReasonValue() != "" {
		return
	}
	cc.markDisconnectReason(disconnectReasonForReadError(sess, err))
}

func disconnectReasonForReadError(sess *Session, err error) string {
	if sess != nil && sess.shutdown.Load() {
		return disconnectReasonShutdown
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection") {
		return disconnectReasonClosed
	}
	return err.Error()
}

func cloneMinOutputSeq(src map[uint32]uint64) map[uint32]uint64 {
	if len(src) == 0 {
		return make(map[uint32]uint64)
	}
	dst := make(map[uint32]uint64, len(src))
	for paneID, seq := range src {
		dst[paneID] = seq
	}
	return dst
}

func cloneMessage(msg *Message) *Message {
	if msg == nil {
		return nil
	}
	cp := *msg
	cp.Input = append([]byte(nil), msg.Input...)
	cp.CmdArgs = append([]string(nil), msg.CmdArgs...)
	cp.RenderData = append([]byte(nil), msg.RenderData...)
	cp.PaneData = append([]byte(nil), msg.PaneData...)
	cp.History = append([]string(nil), msg.History...)
	cp.StyledHistory = proto.CloneStyledLines(msg.StyledHistory)
	return &cp
}

func (cc *clientConn) activeInputPaneForWrite(sess *Session) *mux.Pane {
	return sess.activeInputPaneForWrite(cc)
}

func (cc *clientConn) notePredictionEpoch(paneID uint32, epoch uint32, data []byte) {
	if cc == nil || paneID == 0 || epoch == 0 || !cc.capabilities.PredictionSupported || len(data) == 0 {
		return
	}
	cc.predictions[paneID] = append(cc.predictions[paneID], pendingPredictionEpoch{
		epoch:     epoch,
		remaining: append([]byte(nil), data...),
	})
}

func (cc *clientConn) decoratePaneOutput(msg *Message, paneID uint32) *Message {
	if cc == nil || msg == nil || msg.SourceEpoch != 0 || !cc.capabilities.PredictionSupported {
		return msg
	}
	sourceEpoch := cc.consumePredictionEpoch(paneID, msg.PaneData)
	if sourceEpoch == 0 {
		return msg
	}
	decorated := cloneMessage(msg)
	decorated.SourceEpoch = sourceEpoch
	return decorated
}

func (cc *clientConn) consumePredictionEpoch(paneID uint32, data []byte) uint32 {
	if cc == nil || paneID == 0 || len(data) == 0 {
		return 0
	}
	queue := cc.predictions[paneID]
	if len(queue) == 0 {
		return 0
	}

	latest := uint32(0)
	index := 0
	pos := 0
	matchedPrefix := false
	for index < len(queue) {
		entry := queue[index]
		for len(entry.remaining) > 0 && pos < len(data) && entry.remaining[0] == data[pos] {
			entry.remaining = entry.remaining[1:]
			pos++
			matchedPrefix = true
		}
		queue[index] = entry
		if len(entry.remaining) != 0 {
			break
		}
		latest = entry.epoch
		index++
	}

	switch {
	case latest != 0:
		queue = queue[index:]
	case matchedPrefix:
		cc.predictions[paneID] = queue
		return 0
	default:
		// Force reconciliation on the oldest pending epoch as soon as unrelated
		// PTY output diverges from the predicted stream. There is no separate
		// epoch timeout path today, so leaving the queue untouched here would let
		// the stale shadow emulator mask real server output indefinitely.
		latest = queue[0].epoch
		queue = queue[1:]
	}

	if len(queue) == 0 {
		delete(cc.predictions, paneID)
	} else {
		cc.predictions[paneID] = queue
	}
	return latest
}

// readLoop reads messages from the client and dispatches them to the session.
func (cc *clientConn) readLoop(srv *Server, sess *Session) {
	detachReason := DisconnectReasonSocketError
	defer func() {
		sess.enqueueDetachClient(cc, detachReason)
		cc.Close()
	}()

	for {
		msg, err := cc.reader.ReadMsg()
		if err != nil {
			cc.finalizeDisconnectReason(sess, err)
			return
		}

		switch msg.Type {
		case MsgTypeInput:
			sess.enqueueLiveInputWithEpoch(cc, msg.Input, msg.InputEpoch)

		case MsgTypeResize:
			sess.enqueueResizeClient(cc, msg.Cols, msg.Rows)

		case MsgTypeInputPane:
			sess.enqueueLiveInputPaneFromClient(cc, msg.PaneID, msg.PaneData, msg.InputEpoch)

		case MsgTypeDetach:
			cc.markDisconnectReason(disconnectReasonClientDetach)
			detachReason = DisconnectReasonExplicitDetach
			return

		case MsgTypeCommand:
			cc.handleCommand(srv, sess, msg)

		case MsgTypeCaptureResponse:
			sess.routeCaptureResponse(msg)
		case MsgTypeUIEvent:
			sess.enqueueUIEvent(cc, msg.UIEvent)
		}
	}
}

// handleCommand dispatches CLI commands through the command registry.
func (cc *clientConn) handleCommand(srv *Server, sess *Session, msg *Message) {
	started := time.Now()
	ctx := &CommandContext{
		CommandName: msg.CmdName,
		CC:          cc,
		Srv:         srv,
		Sess:        sess,
		Args:        msg.CmdArgs,
		ActorPaneID: msg.ActorPaneID,
	}
	defer func() {
		if r := recover(); r != nil {
			ctx.auditErr = fmt.Sprintf("internal error: panic in command %q", msg.CmdName)
			if err := sess.logPanic("command_panic", r, debug.Stack()); err != nil {
				ctx.auditErr = err.Error()
			}
			if err := cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: ctx.auditErr}); err != nil && cc.logger != nil {
				cc.logger.Warn("sending panic response failed", "event", "command_panic", "command", msg.CmdName, "error", err)
			}
		}
		sess.logCommandExecution(cc.ID, msg.CmdName, msg.CmdArgs, msg.ActorPaneID, time.Since(started), ctx.auditErr)
	}()
	// Command RPCs are control traffic. Only real terminal interaction
	// (input, resize, focus) should transfer size ownership between clients.
	handler, ok := srv.lookupCommand(msg.CmdName)
	if !ok {
		ctx.auditErr = fmt.Sprintf("unknown command: %s", msg.CmdName)
		if err := cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: ctx.auditErr}); err != nil && cc.logger != nil {
			cc.logger.Warn("sending unknown-command response failed", "event", "command_dispatch", "command", msg.CmdName, "error", err)
		}
		return
	}
	handler(ctx)
}
