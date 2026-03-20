package server

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

// ClientConn manages a single client connection to the server.
type ClientConn struct {
	conn              net.Conn
	ID                string
	displayPanesShown bool
	chooserMode       string
	copyModeShown     bool
	inputIdle         bool
	mu                sync.Mutex
	closed            bool
	cols              int // last reported terminal width
	rows              int // last reported terminal height
}

// NewClientConn wraps a net.Conn for protocol communication.
func NewClientConn(conn net.Conn) *ClientConn {
	return &ClientConn{conn: conn, inputIdle: true}
}

// Send writes a message to the client. Thread-safe.
func (cc *ClientConn) Send(msg *Message) error {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if cc.closed {
		return nil
	}
	return WriteMsg(cc.conn, msg)
}

// Close shuts down the connection.
func (cc *ClientConn) Close() {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if !cc.closed {
		cc.closed = true
		cc.conn.Close()
	}
}

// readLoop reads messages from the client and dispatches them to the session.
func (cc *ClientConn) readLoop(srv *Server, sess *Session) {
	defer func() {
		sess.enqueueDetachClient(cc)
		cc.Close()
	}()

	for {
		msg, err := ReadMsg(cc.conn)
		if err != nil {
			return
		}

		switch msg.Type {
		case MsgTypeInput:
			pane, err := enqueueSessionQuery(sess, func(sess *Session) (*mux.Pane, error) {
				w := sess.ActiveWindow()
				if w == nil {
					return nil, nil
				}
				return w.ActivePane, nil
			})
			if err == nil && pane != nil {
				pane.Write(msg.Input)
			}

		case MsgTypeResize:
			sess.enqueueResizeClient(cc, msg.Cols, msg.Rows)

		case MsgTypeInputPane:
			// Targeted input for a specific pane (used by remote proxy connections)
			pane, err := enqueueSessionQuery(sess, func(sess *Session) (*mux.Pane, error) {
				return sess.findPaneByID(msg.PaneID), nil
			})
			if err == nil && pane != nil {
				pane.Write(msg.PaneData)
			}

		case MsgTypeDetach:
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

func (cc *ClientConn) resolvePaneWindowLocked(sess *Session, cmdName string, args []string) (*mux.Pane, *mux.Window, error) {
	return sess.resolvePaneWindow(cmdName, args)
}

// handleCommand dispatches CLI commands through the command registry.
func (cc *ClientConn) handleCommand(srv *Server, sess *Session, msg *Message) {
	handler, ok := commandRegistry[msg.CmdName]
	if !ok {
		cc.Send(&Message{Type: MsgTypeCmdResult,
			CmdErr: fmt.Sprintf("unknown command: %s", msg.CmdName)})
		return
	}
	handler(&CommandContext{CC: cc, Srv: srv, Sess: sess, Args: msg.CmdArgs})
}

// splitRemotePane prepares a proxy pane connected to a remote host, then
// inserts it into the active window through the session event loop.
func (cc *ClientConn) splitRemotePane(srv *Server, sess *Session, hostName string, dir mux.SplitDir, rootLevel bool) (*mux.Pane, error) {
	type activeWindowSize struct {
		width  int
		height int
	}

	size, err := enqueueSessionQuery(sess, func(sess *Session) (activeWindowSize, error) {
		w := sess.ActiveWindow()
		if w == nil {
			return activeWindowSize{}, fmt.Errorf("no window")
		}
		return activeWindowSize{width: w.Width, height: w.Height}, nil
	})
	if err != nil {
		return nil, err
	}

	pane, err := sess.prepareRemotePane(srv, hostName, size.width, mux.PaneContentHeight(size.height))
	if err != nil {
		return nil, err
	}

	res := sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		if err := sess.insertPreparedPaneIntoActiveWindow(pane, dir, rootLevel); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{broadcastLayout: true}
	})
	if res.err != nil {
		if sess.RemoteManager != nil {
			sess.RemoteManager.RemovePane(pane.ID)
		}
		pane.Close()
		return nil, res.err
	}
	if res.broadcastLayout {
		sess.broadcastLayout()
	}

	return pane, nil
}

// resolvePane validates args and resolves a pane by name or ID.
// Searches active window first, then all windows. Sends an error to the client
// on failure.
func (cc *ClientConn) resolvePane(sess *Session, cmdName string, args []string) *mux.Pane {
	if len(args) < 1 {
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("usage: %s <pane>", cmdName)})
		return nil
	}
	pane, err := enqueueSessionQuery(sess, func(sess *Session) (*mux.Pane, error) {
		pane, _, err := sess.resolvePaneAcrossWindows(args[0])
		return pane, err
	})
	if err != nil {
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
		return nil
	}
	return pane
}

// resolvePaneAcrossWindows resolves a pane reference, searching the active
// window first, then all other windows.
func (cc *ClientConn) resolvePaneAcrossWindows(sess *Session, cmdName string, ref string) *mux.Pane {
	pane, err := enqueueSessionQuery(sess, func(sess *Session) (*mux.Pane, error) {
		pane, _, err := sess.resolvePaneAcrossWindows(ref)
		return pane, err
	})
	if err != nil {
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
		return nil
	}
	return pane
}

// resolvePaneAcrossWindowsLocked resolves a pane reference, searching the active
// window first, then all other windows, then the flat pane registry.
func (cc *ClientConn) resolvePaneAcrossWindowsLocked(sess *Session, ref string) (*mux.Pane, *mux.Window, error) {
	return sess.resolvePaneAcrossWindows(ref)
}

// parseWaitArgs extracts --after and --timeout flags from command arguments.
// Used by wait-layout and wait-clipboard which share the same flag syntax.
func parseWaitArgs(args []string) (afterGen uint64, timeout time.Duration, err error) {
	timeout = 3 * time.Second
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--after":
			if i+1 < len(args) {
				i++
				afterGen, err = strconv.ParseUint(args[i], 10, 64)
				if err != nil {
					return 0, 0, fmt.Errorf("invalid generation: %s", args[i])
				}
			}
		case "--timeout":
			if i+1 < len(args) {
				i++
				timeout, err = time.ParseDuration(args[i])
				if err != nil {
					return 0, 0, fmt.Errorf("invalid timeout: %s", args[i])
				}
			}
		}
	}
	return afterGen, timeout, nil
}

// parseTimeout extracts --timeout from args starting at the given offset.
// Returns the parsed duration or the provided default.
// Used by wait-for, wait-idle, and wait-busy.
func parseTimeout(args []string, startIdx int, defaultTimeout time.Duration) (time.Duration, error) {
	for i := startIdx; i < len(args); i++ {
		if args[i] == "--timeout" && i+1 < len(args) {
			i++
			d, err := time.ParseDuration(args[i])
			if err != nil {
				return 0, fmt.Errorf("invalid timeout: %s", args[i])
			}
			return d, nil
		}
	}
	return defaultTimeout, nil
}

// parseKey converts a key name to its byte representation.
// Supports special key names (Enter, Tab, C-x, Escape, etc.)
// and literal text (sent as-is).
func parseKey(key string) []byte {
	// Check special key names (case-sensitive, matching tmux conventions)
	if b, ok := specialKeys[key]; ok {
		return b
	}

	// C-x / C-X → Ctrl+letter (ASCII control code)
	if len(key) == 3 && (key[0] == 'C' || key[0] == 'c') && key[1] == '-' {
		ch := key[2]
		if ch >= 'a' && ch <= 'z' {
			return []byte{ch - 'a' + 1}
		}
		if ch >= 'A' && ch <= 'Z' {
			return []byte{ch - 'A' + 1}
		}
	}

	// M-x / M-X → Alt+key (ESC prefix)
	if len(key) == 3 && (key[0] == 'M' || key[0] == 'm') && key[1] == '-' {
		return []byte{0x1b, key[2]}
	}

	// Literal text
	return []byte(key)
}

// specialKeys maps tmux-compatible key names to byte sequences.
var specialKeys = map[string][]byte{
	"Enter":    {'\r'},
	"Tab":      {'\t'},
	"Escape":   {0x1b},
	"Space":    {' '},
	"BSpace":   {0x7f},
	"Up":       {0x1b, '[', 'A'},
	"Down":     {0x1b, '[', 'B'},
	"Right":    {0x1b, '[', 'C'},
	"Left":     {0x1b, '[', 'D'},
	"Home":     {0x1b, '[', 'H'},
	"End":      {0x1b, '[', 'F'},
	"PageUp":   {0x1b, '[', '5', '~'},
	"PageDown": {0x1b, '[', '6', '~'},
	"Delete":   {0x1b, '[', '3', '~'},
	"Insert":   {0x1b, '[', '2', '~'},
}

func dirName(d mux.SplitDir) string {
	if d == mux.SplitVertical {
		return "vertical"
	}
	return "horizontal"
}

// parseEventsArgs parses --filter, --pane, --host, and --client flags for the events command.
func parseEventsArgs(args []string) eventFilter {
	var f eventFilter
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--filter":
			if i+1 < len(args) {
				i++
				f.Types = strings.Split(args[i], ",")
			}
		case "--pane":
			if i+1 < len(args) {
				i++
				f.PaneName = args[i]
			}
		case "--host":
			if i+1 < len(args) {
				i++
				f.Host = args[i]
			}
		case "--client":
			if i+1 < len(args) {
				i++
				f.ClientID = args[i]
			}
		}
	}
	return f
}
