package server

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/render"
)

// ClientConn manages a single client connection to the server.
type ClientConn struct {
	conn   net.Conn
	mu     sync.Mutex
	closed bool
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
		sess.removeClient(cc)
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
			sess.mu.Lock()
			// Resize all windows to match the terminal
			layoutH := msg.Rows - render.GlobalBarHeight
			for _, w := range sess.Windows {
				w.Resize(msg.Cols, layoutH)
			}
			sess.mu.Unlock()
			sess.broadcastLayout()

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
		}
	}
}

// withPaneWindow resolves a pane from command args, finds its containing window,
// and runs fn under the session lock. On success, it broadcasts the layout update
// and sends the result to the client. On error, it sends the error message.
func (cc *ClientConn) withPaneWindow(sess *Session, cmdName string, args []string,
	fn func(pane *mux.Pane, w *mux.Window) (string, error)) {
	sess.mu.Lock()
	pane := cc.resolvePane(sess, cmdName, args)
	if pane == nil {
		sess.mu.Unlock()
		return
	}
	w := sess.FindWindowByPaneID(pane.ID)
	if w == nil {
		sess.mu.Unlock()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "pane not in any window"})
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

// createNewWindow creates a new window with one pane and switches to it.
func (cc *ClientConn) createNewWindow(srv *Server, sess *Session, name string) {
	sess.mu.Lock()
	w := sess.ActiveWindow()
	if w == nil {
		sess.mu.Unlock()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no session"})
		return
	}

	cols, layoutH := w.Width, w.Height
	paneH := mux.PaneContentHeight(layoutH)

	pane, err := sess.createPane(srv, cols, paneH)
	if err != nil {
		sess.mu.Unlock()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
		return
	}

	winID := sess.windowCounter.Add(1)
	newWin := mux.NewWindow(pane, cols, layoutH)
	newWin.ID = winID
	if name != "" {
		newWin.Name = name
	} else {
		newWin.Name = fmt.Sprintf(WindowNameFormat, winID)
	}
	sess.Windows = append(sess.Windows, newWin)
	sess.ActiveWindowID = winID
	sess.mu.Unlock()

	pane.Start()
	sess.broadcastLayout()
	cc.Send(&Message{Type: MsgTypeCmdResult,
		CmdOutput: fmt.Sprintf("Created %s\n", newWin.Name)})
}

// splitRemotePane creates a proxy pane connected to a remote host, inserts it
// into the active window's layout, and triggers a render. Returns the pane, or nil on error.
func (cc *ClientConn) splitRemotePane(srv *Server, sess *Session, hostName string, dir mux.SplitDir, rootLevel bool) *mux.Pane {
	sess.mu.Lock()
	w := sess.ActiveWindow()
	if w == nil {
		sess.mu.Unlock()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no window"})
		return nil
	}
	initW, initH := w.Width, w.Height
	sess.mu.Unlock()

	// createRemotePane must be called without holding s.mu (SSH calls inside)
	pane, err := sess.createRemotePane(srv, hostName, initW, mux.PaneContentHeight(initH))
	if err != nil {
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
		return nil
	}

	sess.mu.Lock()
	w = sess.ActiveWindow()
	if w == nil {
		sess.mu.Unlock()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no window"})
		return nil
	}
	if rootLevel {
		_, err = w.SplitRoot(dir, pane)
	} else {
		_, err = w.Split(dir, pane)
	}
	sess.mu.Unlock()

	if err != nil {
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
		return nil
	}

	// No pane.Start() needed — proxy panes don't have a readLoop/waitLoop
	sess.broadcastLayout()
	return pane
}

// splitNewPane creates a pane, inserts it into the active window's layout,
// starts it, and triggers a render. Returns the new pane, or nil on error.
func (cc *ClientConn) splitNewPane(srv *Server, sess *Session, meta mux.PaneMeta, dir mux.SplitDir, rootLevel bool) *mux.Pane {
	sess.mu.Lock()
	w := sess.ActiveWindow()
	if w == nil {
		sess.mu.Unlock()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no window"})
		return nil
	}

	initW, initH := w.Width, w.Height
	var (
		pane *mux.Pane
		err  error
	)
	if meta.Name != "" {
		pane, err = sess.createPaneWithMeta(srv, meta, initW, mux.PaneContentHeight(initH))
	} else {
		pane, err = sess.createPane(srv, initW, mux.PaneContentHeight(initH))
	}
	if err != nil {
		sess.mu.Unlock()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
		return nil
	}

	if rootLevel {
		_, err = w.SplitRoot(dir, pane)
	} else {
		_, err = w.Split(dir, pane)
	}
	if err != nil {
		sess.mu.Unlock()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
		return nil
	}
	sess.mu.Unlock()

	pane.Start()
	sess.broadcastLayout()
	return pane
}

// resolvePane validates args and resolves a pane by name or ID.
// Searches active window first, then all windows.
// Caller must hold sess.mu. Sends an error to the client on failure.
func (cc *ClientConn) resolvePane(sess *Session, cmdName string, args []string) *mux.Pane {
	if len(args) < 1 {
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("usage: %s <pane>", cmdName)})
		return nil
	}
	return cc.resolvePaneAcrossWindows(sess, cmdName, args[0])
}

// resolvePaneAcrossWindows resolves a pane reference, searching the active
// window first, then all other windows.
// Caller must hold sess.mu.
func (cc *ClientConn) resolvePaneAcrossWindows(sess *Session, cmdName string, ref string) *mux.Pane {
	w := sess.ActiveWindow()
	if w == nil {
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no session"})
		return nil
	}
	// Search active window first
	if pane := w.ResolvePane(ref); pane != nil {
		return pane
	}
	// Search all other windows
	for _, win := range sess.Windows {
		if win.ID == w.ID {
			continue
		}
		if pane := win.ResolvePane(ref); pane != nil {
			return pane
		}
	}
	cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("pane %q not found", ref)})
	return nil
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
	if d == mux.SplitHorizontal {
		return "horizontal"
	}
	return "vertical"
}

// parseEventsArgs parses --filter, --pane, and --host flags for the events command.
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
		}
	}
	return f
}
