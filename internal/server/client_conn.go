package server

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

const (
	disconnectReasonClientDetach = "client detach"
	disconnectReasonClosed       = "connection closed"
	disconnectReasonShutdown     = "server shutdown"
)

// clientConn manages a single client connection to the server.
type clientConn struct {
	conn               net.Conn
	ID                 string
	displayPanesShown  bool
	prefixMessageShown bool
	chooserMode        string
	copyModeShown      bool
	inputIdle          bool
	uiGeneration       uint64
	cols               int // last reported terminal width
	rows               int // last reported terminal height
	writer             *clientWriter
	typeKeyQueue       *pacedInputQueue
	capabilities       proto.ClientCapabilities
	disconnectReasonMu sync.Mutex
	disconnectReason   string
}

type pendingMessage struct {
	msg       *Message
	paneID    uint32
	outputSeq uint64
}

// newClientConn wraps a net.Conn for protocol communication.
func newClientConn(conn net.Conn) *clientConn {
	cc := &clientConn{
		conn:      conn,
		inputIdle: true,
	}
	cc.writer = newClientWriter(conn)
	return cc
}

func (cc *clientConn) setNegotiatedCapabilities(caps proto.ClientCapabilities) {
	cc.capabilities = caps
}

func (cc *clientConn) capabilitySummary() string {
	return cc.capabilities.Summary()
}

// Send writes a message to the client. Thread-safe.
func (cc *clientConn) Send(msg *Message) error {
	return cc.ensureWriter().send(msg)
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

func (cc *clientConn) initTypeKeyQueue() {
	if cc.typeKeyQueue != nil {
		return
	}
	cc.typeKeyQueue = newPacedInputQueue("client "+cc.ID, func(data []byte) error {
		return cc.Send(&Message{Type: MsgTypeTypeKeys, Input: data})
	})
}

func (cc *clientConn) startBootstrap() {
	cc.ensureWriter().startBootstrap()
}

func (cc *clientConn) finishBootstrap(minOutputSeq map[uint32]uint64) {
	cc.ensureWriter().finishBootstrap(minOutputSeq)
}

func (cc *clientConn) sendBroadcast(msg *Message) {
	cc.ensureWriter().sendBroadcast(msg)
}

func (cc *clientConn) sendBroadcastSync(msg *Message) {
	cc.ensureWriter().sendBroadcastSync(msg)
}

func (cc *clientConn) sendPaneOutput(msg *Message, paneID uint32, seq uint64) {
	cc.ensureWriter().sendPaneOutput(msg, paneID, seq)
}

func (cc *clientConn) sendPaneMessage(msg *Message) {
	cc.ensureWriter().sendPaneMessage(msg)
}

func (cc *clientConn) isBootstrapping() bool {
	return cc.ensureWriter().isBootstrapping()
}

func (cc *clientConn) ensureWriter() *clientWriter {
	return cc.writer
}

func (cc *clientConn) markDisconnectReason(reason string) {
	if cc == nil || reason == "" {
		return
	}
	cc.disconnectReasonMu.Lock()
	defer cc.disconnectReasonMu.Unlock()
	if cc.disconnectReason == "" {
		cc.disconnectReason = reason
	}
}

func (cc *clientConn) disconnectReasonValue() string {
	if cc == nil {
		return ""
	}
	cc.disconnectReasonMu.Lock()
	defer cc.disconnectReasonMu.Unlock()
	return cc.disconnectReason
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
	return &cp
}

func (cc *clientConn) activeInputPaneForWrite(sess *Session) *mux.Pane {
	return sess.activeInputPaneForWrite(cc)
}

// readLoop reads messages from the client and dispatches them to the session.
func (cc *clientConn) readLoop(srv *Server, sess *Session) {
	detachReason := DisconnectReasonSocketError
	defer func() {
		sess.enqueueDetachClient(cc, detachReason)
		cc.Close()
	}()

	for {
		msg, err := ReadMsg(cc.conn)
		if err != nil {
			cc.finalizeDisconnectReason(sess, err)
			return
		}

		switch msg.Type {
		case MsgTypeInput:
			if pane := cc.activeInputPaneForWrite(sess); pane != nil {
				if err := sess.enqueueLivePaneInput(pane, msg.Input); err != nil && !errors.Is(err, errPacedInputClosed) {
					log.Printf("[amux] live input %s: %v", pane.Meta.Name, err)
				}
			}

		case MsgTypeResize:
			sess.enqueueResizeClient(cc, msg.Cols, msg.Rows)

		case MsgTypeInputPane:
			// Targeted input for a specific pane (used by remote proxy connections)
			pane := sess.ensureInputRouter().paneByID(msg.PaneID)
			if pane == nil {
				var err error
				pane, err = enqueueSessionQuery(sess, func(sess *Session) (*mux.Pane, error) {
					return sess.findPaneByID(msg.PaneID), nil
				})
				if err != nil {
					break
				}
			}
			if pane != nil {
				if err := sess.enqueueLivePaneInput(pane, msg.PaneData); err != nil && !errors.Is(err, errPacedInputClosed) {
					log.Printf("[amux] live input %s: %v", pane.Meta.Name, err)
				}
			}

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
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[amux] panic in command %q: %v\n%s", msg.CmdName, r, debug.Stack())
			cc.Send(&Message{Type: MsgTypeCmdResult,
				CmdErr: fmt.Sprintf("internal error: panic in command %q", msg.CmdName)})
		}
	}()
	sess.enqueueClientActivity(cc)
	handler, ok := srv.lookupCommand(msg.CmdName)
	if !ok {
		cc.Send(&Message{Type: MsgTypeCmdResult,
			CmdErr: fmt.Sprintf("unknown command: %s", msg.CmdName)})
		return
	}
	handler(&CommandContext{CC: cc, Srv: srv, Sess: sess, Args: msg.CmdArgs, ActorPaneID: msg.ActorPaneID})
}

// splitRemotePane prepares a proxy pane connected to a remote host, then
// inserts it into the active window through the session event loop.
func (cc *clientConn) splitRemotePane(sess *Session, hostName string, dir mux.SplitDir, rootLevel bool, name string, keepFocus bool) (*mux.Pane, error) {
	type activeWindowSize struct {
		width  int
		height int
	}

	size, err := enqueueSessionQuery(sess, func(sess *Session) (activeWindowSize, error) {
		w := sess.activeWindow()
		if w == nil {
			return activeWindowSize{}, fmt.Errorf("no window")
		}
		return activeWindowSize{width: w.Width, height: w.Height}, nil
	})
	if err != nil {
		return nil, err
	}

	pane, err := sess.prepareRemotePane(hostName, size.width, mux.PaneContentHeight(size.height))
	if err != nil {
		return nil, err
	}
	if name != "" {
		pane.Meta.Name = name
	}

	res := sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		if err := sess.insertPreparedPaneIntoActiveWindow(pane, dir, rootLevel, keepFocus); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{broadcastLayout: true}
	})
	if res.err != nil {
		pane.Close()
		return nil, res.err
	}

	return pane, nil
}

// eventsArgs holds parsed arguments for the events command.
type eventsArgs struct {
	filter   eventFilter
	throttle time.Duration
}

// parseEventsArgs parses --filter, --pane, --host, --client, and --throttle flags for the events command.
func parseEventsArgs(args []string) eventsArgs {
	ea := eventsArgs{throttle: DefaultEventThrottle}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--filter":
			if i+1 < len(args) {
				i++
				ea.filter.Types = strings.Split(args[i], ",")
			}
		case "--pane":
			if i+1 < len(args) {
				i++
				ea.filter.PaneName = args[i]
			}
		case "--host":
			if i+1 < len(args) {
				i++
				ea.filter.Host = args[i]
			}
		case "--client":
			if i+1 < len(args) {
				i++
				ea.filter.ClientID = args[i]
			}
		case "--throttle":
			if i+1 < len(args) {
				i++
				if d, err := time.ParseDuration(args[i]); err == nil {
					ea.throttle = d
				}
			}
		}
	}
	return ea
}
