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
	mu                sync.Mutex
	closed            bool
	cols              int // last reported terminal width
	rows              int // last reported terminal height
}

// NewClientConn wraps a net.Conn for protocol communication.
func NewClientConn(conn net.Conn) *ClientConn {
	return &ClientConn{conn: conn}
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
			sess.mu.Lock()
			w := sess.ActiveWindow()
			if w != nil && w.ActivePane != nil {
				w.ActivePane.Write(msg.Input)
			}
			sess.mu.Unlock()

		case MsgTypeResize:
			sess.enqueueResizeClient(cc, msg.Cols, msg.Rows)

		case MsgTypeInputPane:
			// Targeted input for a specific pane (used by remote proxy connections)
			sess.mu.Lock()
			for _, p := range sess.Panes {
				if p.ID == msg.PaneID {
					p.Write(msg.PaneData)
					break
				}
			}
			sess.mu.Unlock()

		case MsgTypeDetach:
			return

		case MsgTypeCommand:
			cc.handleCommand(srv, sess, msg)

		case MsgTypeCaptureResponse:
			sess.routeCaptureResponse(msg)
		case MsgTypeUIEvent:
			sess.mu.Lock()
			changed, err := cc.applyUIEvent(msg.UIEvent)
			clientID := cc.ID
			sess.mu.Unlock()
			if err != nil {
				cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
				continue
			}
			if changed {
				sess.events.Emit(Event{Type: msg.UIEvent, ClientID: clientID})
			}
		}
	}
}

// withPaneWindow resolves a pane from command args, finds its containing window,
// and runs fn under the session lock. On success, it broadcasts the layout update
// and sends the result to the client. On error, it sends the error message.
func (cc *ClientConn) withPaneWindow(sess *Session, cmdName string, args []string,
	fn func(pane *mux.Pane, w *mux.Window) (string, error)) {
	sess.mu.Lock()
	pane, w, err := cc.resolvePaneWindowLocked(sess, cmdName, args)
	if err != nil {
		sess.mu.Unlock()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
		return
	}
	result, err := fn(pane, w)
	sess.mu.Unlock()
	if err != nil {
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
		return
	}
	sess.broadcastLayout()
	cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: result})
}

func (cc *ClientConn) resolvePaneWindowLocked(sess *Session, cmdName string, args []string) (*mux.Pane, *mux.Window, error) {
	if len(args) < 1 {
		return nil, nil, fmt.Errorf("usage: %s <pane>", cmdName)
	}
	pane, w, err := cc.resolvePaneAcrossWindowsLocked(sess, args[0])
	if err != nil {
		return nil, nil, err
	}
	if w == nil {
		return nil, nil, fmt.Errorf("pane not in any window")
	}
	return pane, w, nil
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
	sess.mu.Lock()
	w := sess.ActiveWindow()
	if w == nil {
		sess.mu.Unlock()
		return nil, fmt.Errorf("no window")
	}
	initW, initH := w.Width, w.Height
	sess.mu.Unlock()

	pane, err := sess.prepareRemotePane(srv, hostName, initW, mux.PaneContentHeight(initH))
	if err != nil {
		return nil, err
	}

	res := sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		sess.mu.Lock()
		defer sess.mu.Unlock()
		if err := sess.insertPreparedPaneIntoActiveWindowLocked(pane, dir, rootLevel); err != nil {
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
// Searches active window first, then all windows.
// Caller must hold sess.mu. Sends an error to the client on failure.
func (cc *ClientConn) resolvePane(sess *Session, cmdName string, args []string) *mux.Pane {
	if len(args) < 1 {
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("usage: %s <pane>", cmdName)})
		return nil
	}
	pane, _, err := cc.resolvePaneAcrossWindowsLocked(sess, args[0])
	if err != nil {
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
		return nil
	}
	return pane
}

// resolvePaneAcrossWindows resolves a pane reference, searching the active
// window first, then all other windows.
// Caller must hold sess.mu.
func (cc *ClientConn) resolvePaneAcrossWindows(sess *Session, cmdName string, ref string) *mux.Pane {
	pane, _, err := cc.resolvePaneAcrossWindowsLocked(sess, ref)
	if err != nil {
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
		return nil
	}
	return pane
}

// resolvePaneAcrossWindowsLocked resolves a pane reference, searching the active
// window first, then all other windows, then the flat pane registry.
// Caller must hold sess.mu.
func (cc *ClientConn) resolvePaneAcrossWindowsLocked(sess *Session, ref string) (*mux.Pane, *mux.Window, error) {
	w := sess.ActiveWindow()
	if w == nil {
		return nil, nil, fmt.Errorf("no session")
	}
	// Search active window first
	if pane := w.ResolvePane(ref); pane != nil {
		return pane, w, nil
	}
	// Search all other windows
	for _, win := range sess.Windows {
		if win.ID == w.ID {
			continue
		}
		if pane := win.ResolvePane(ref); pane != nil {
			return pane, win, nil
		}
	}
	// Fall back: search flat pane registry for orphaned/dormant panes
	if pane := sess.findPaneByRef(ref); pane != nil {
		return pane, sess.FindWindowByPaneID(pane.ID), nil
	}
	return nil, nil, fmt.Errorf("pane %q not found", ref)
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
