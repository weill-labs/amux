package server

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/weill-labs/amux/internal/mux"
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
			if sess.Window != nil && sess.Window.ActivePane != nil {
				sess.Window.ActivePane.Write(msg.Input)
			}
			sess.mu.Unlock()

		case MsgTypeResize:
			sess.mu.Lock()
			if sess.Window != nil {
				// Layout height = terminal height minus global status bar
				layoutH := msg.Rows - 1
				sess.Window.Resize(msg.Cols, layoutH)
			}
			sess.mu.Unlock()
			sess.broadcastLayout()

		case MsgTypeDetach:
			return

		case MsgTypeCommand:
			cc.handleCommand(srv, sess, msg)
		}
	}
}

// handleCommand processes one-shot CLI commands (list, split, etc.).
func (cc *ClientConn) handleCommand(srv *Server, sess *Session, msg *Message) {
	switch msg.CmdName {
	case "list":
		sess.mu.Lock()
		var output string
		if len(sess.Panes) == 0 {
			output = "No panes.\n"
		} else {
			output = fmt.Sprintf("%-6s %-20s %-15s %s\n", "PANE", "NAME", "HOST", "TASK")
			for _, p := range sess.Panes {
				active := " "
				if sess.Window != nil && sess.Window.ActivePane != nil && sess.Window.ActivePane.ID == p.ID {
					active = "*"
				}
				output += fmt.Sprintf("%-6s %-20s %-15s %s\n",
					fmt.Sprintf("%s%d", active, p.ID),
					p.Meta.Name, p.Meta.Host, p.Meta.Task)
			}
		}
		sess.mu.Unlock()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: output})

	case "split":
		rootLevel := false
		dir := mux.SplitHorizontal
		for _, arg := range msg.CmdArgs {
			switch arg {
			case "v":
				dir = mux.SplitVertical
			case "root":
				rootLevel = true
			}
		}
		pane := cc.splitNewPane(srv, sess, mux.PaneMeta{}, dir, rootLevel)
		if pane != nil {
			cc.Send(&Message{Type: MsgTypeCmdResult,
				CmdOutput: fmt.Sprintf("Split %s: new pane %s\n", dirName(dir), pane.Meta.Name)})
		}

	case "focus":
		direction := "next"
		if len(msg.CmdArgs) > 0 {
			direction = msg.CmdArgs[0]
		}

		sess.mu.Lock()
		if sess.Window == nil {
			sess.mu.Unlock()
			return
		}

		switch direction {
		case "next", "left", "right", "up", "down":
			sess.Window.Focus(direction)
			sess.mu.Unlock()
		default:
			// Treat as pane name or ID
			pane := sess.Window.ResolvePane(direction)
			if pane == nil {
				sess.mu.Unlock()
				cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("pane %q not found", direction)})
				return
			}
			sess.Window.ActivePane = pane
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: fmt.Sprintf("Focused %s\n", pane.Meta.Name)})
		}

		sess.broadcastLayout()

	case "capture":
		// amux capture [--ansi|--colors] [pane] — full screen or single pane
		includeANSI := false
		colorMap := false
		var paneRef string
		for _, arg := range msg.CmdArgs {
			switch arg {
			case "--ansi":
				includeANSI = true
			case "--colors":
				colorMap = true
			default:
				paneRef = arg
			}
		}

		if paneRef != "" {
			// Single pane capture (replaces old "output" command)
			sess.mu.Lock()
			if sess.Window == nil {
				sess.mu.Unlock()
				cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no session"})
				return
			}
			pane := sess.Window.ResolvePane(paneRef)
			if pane == nil {
				sess.mu.Unlock()
				cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("pane %q not found", paneRef)})
				return
			}
			out := pane.Output(DefaultOutputLines)
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: out + "\n"})
		} else {
			// Full composited screen capture
			var out string
			if colorMap {
				out = sess.renderColorMap()
			} else {
				out = sess.renderCapture(!includeANSI)
			}
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: out})
		}

	case "spawn":
		// Parse: spawn --name NAME [--host HOST] [--task TASK]
		meta := mux.PaneMeta{Host: mux.DefaultHost}
		for i := 0; i < len(msg.CmdArgs)-1; i += 2 {
			switch msg.CmdArgs[i] {
			case "--name":
				meta.Name = msg.CmdArgs[i+1]
			case "--host":
				meta.Host = msg.CmdArgs[i+1]
			case "--task":
				meta.Task = msg.CmdArgs[i+1]
			case "--color":
				meta.Color = msg.CmdArgs[i+1]
			}
		}
		if meta.Name == "" {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "--name is required"})
			return
		}
		pane := cc.splitNewPane(srv, sess, meta, mux.SplitHorizontal, false)
		if pane != nil {
			cc.Send(&Message{Type: MsgTypeCmdResult,
				CmdOutput: fmt.Sprintf("Spawned %s in pane %d\n", meta.Name, pane.ID)})
		}

	case "zoom":
		sess.mu.Lock()
		if sess.Window == nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no session"})
			return
		}
		// Resolve target pane: explicit arg or active pane
		var pane *mux.Pane
		if len(msg.CmdArgs) > 0 {
			pane = sess.Window.ResolvePane(msg.CmdArgs[0])
			if pane == nil {
				sess.mu.Unlock()
				cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("pane %q not found", msg.CmdArgs[0])})
				return
			}
		} else {
			pane = sess.Window.ActivePane
		}
		if pane == nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no active pane"})
			return
		}
		willUnzoom := sess.Window.ZoomedPaneID == pane.ID
		err := sess.Window.Zoom(pane.ID)
		sess.mu.Unlock()
		if err != nil {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
			return
		}
		sess.broadcastLayout()
		verb := "Zoomed"
		if willUnzoom {
			verb = "Unzoomed"
		}
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: fmt.Sprintf("%s %s\n", verb, pane.Meta.Name)})

	case "minimize":
		sess.mu.Lock()
		pane := cc.resolvePane(sess, "minimize", msg.CmdArgs)
		if pane == nil {
			sess.mu.Unlock()
			return
		}
		err := sess.Window.Minimize(pane.ID)
		sess.mu.Unlock()
		if err != nil {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
			return
		}
		sess.broadcastLayout()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: fmt.Sprintf("Minimized %s\n", pane.Meta.Name)})

	case "restore":
		sess.mu.Lock()
		pane := cc.resolvePane(sess, "restore", msg.CmdArgs)
		if pane == nil {
			sess.mu.Unlock()
			return
		}
		err := sess.Window.Restore(pane.ID)
		sess.mu.Unlock()
		if err != nil {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
			return
		}
		sess.broadcastLayout()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: fmt.Sprintf("Restored %s\n", pane.Meta.Name)})

	case "kill":
		sess.mu.Lock()
		pane := cc.resolvePane(sess, "kill", msg.CmdArgs)
		if pane == nil {
			sess.mu.Unlock()
			return
		}
		if len(sess.Panes) <= 1 {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "cannot kill last pane"})
			return
		}
		paneID := pane.ID
		paneName := pane.Meta.Name
		// Remove from list BEFORE closing so onExit sees it's gone (C2 fix)
		sess.removePane(paneID)
		sess.Window.ClosePane(paneID)
		sess.mu.Unlock()
		pane.Close()

		sess.broadcastLayout()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: fmt.Sprintf("Killed %s\n", paneName)})

	case "status":
		sess.mu.Lock()
		total := len(sess.Panes)
		minimized := 0
		for _, p := range sess.Panes {
			if p.Meta.Minimized {
				minimized++
			}
		}
		zoomed := ""
		if sess.Window != nil && sess.Window.ZoomedPaneID != 0 {
			for _, p := range sess.Panes {
				if p.ID == sess.Window.ZoomedPaneID {
					zoomed = p.Meta.Name
					break
				}
			}
		}
		sess.mu.Unlock()
		active := total - minimized
		statusLine := fmt.Sprintf("panes: %d total, %d active, %d minimized", total, active, minimized)
		if zoomed != "" {
			statusLine += fmt.Sprintf(", %s zoomed", zoomed)
		}
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: statusLine + "\n"})

	case "resize-border":
		// resize-border <x> <y> <delta>
		if len(msg.CmdArgs) < 3 {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "usage: resize-border <x> <y> <delta>"})
			return
		}
		x, err1 := strconv.Atoi(msg.CmdArgs[0])
		y, err2 := strconv.Atoi(msg.CmdArgs[1])
		delta, err3 := strconv.Atoi(msg.CmdArgs[2])
		if err1 != nil || err2 != nil || err3 != nil {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "resize-border: invalid arguments"})
			return
		}
		sess.mu.Lock()
		if sess.Window != nil {
			sess.Window.ResizeBorder(x, y, delta)
		}
		sess.mu.Unlock()
		sess.broadcastLayout()

	case "reload-server":
		execPath, err := os.Executable()
		if err != nil {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("reload: %v", err)})
			return
		}
		execPath, err = filepath.EvalSymlinks(execPath)
		if err != nil {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("reload: %v", err)})
			return
		}
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: "Server reloading...\n"})
		// Reload replaces the process via exec — doesn't return on success
		if err := srv.Reload(execPath); err != nil {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
		}

	default:
		cc.Send(&Message{Type: MsgTypeCmdResult,
			CmdErr: fmt.Sprintf("unknown command: %s", msg.CmdName)})
	}
}

// splitNewPane creates a pane, inserts it into the layout, starts it, and
// triggers a render. Returns the new pane, or nil if an error was sent.
func (cc *ClientConn) splitNewPane(srv *Server, sess *Session, meta mux.PaneMeta, dir mux.SplitDir, rootLevel bool) *mux.Pane {
	sess.mu.Lock()
	if sess.Window == nil {
		sess.mu.Unlock()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no window"})
		return nil
	}

	initW, initH := sess.Window.Width, sess.Window.Height
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
		_, err = sess.Window.SplitRoot(dir, pane)
	} else {
		_, err = sess.Window.Split(dir, pane)
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
// Caller must hold sess.mu. Sends an error to the client on failure.
func (cc *ClientConn) resolvePane(sess *Session, cmdName string, args []string) *mux.Pane {
	if len(args) < 1 {
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("usage: %s <pane>", cmdName)})
		return nil
	}
	if sess.Window == nil {
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no session"})
		return nil
	}
	pane := sess.Window.ResolvePane(args[0])
	if pane == nil {
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("pane %q not found", args[0])})
		return nil
	}
	return pane
}

func dirName(d mux.SplitDir) string {
	if d == mux.SplitHorizontal {
		return "horizontal"
	}
	return "vertical"
}
