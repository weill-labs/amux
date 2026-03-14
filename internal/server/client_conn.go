package server

import (
	"fmt"
	"net"
	"sync"

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
			if sess.Window != nil && sess.Window.ActivePane != nil {
				sess.Window.ActivePane.Write(msg.Input)
			}
			sess.mu.Unlock()

		case MsgTypeResize:
			sess.mu.Lock()
			if sess.Window != nil {
				sess.compositor.Resize(msg.Cols, msg.Rows)
				sess.Window.Resize(msg.Cols, sess.compositor.LayoutHeight())
			}
			sess.mu.Unlock()
			sess.renderAndBroadcast()

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

		sess.mu.Lock()
		if sess.Window == nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no window"})
			return
		}

		// Initial PTY size (will be resized by Split/SplitRoot)
		initW, initH := sess.Window.Width, sess.Window.Height
		newPane, err := sess.createPane(srv, initW, render.PaneContentHeight(initH))
		if err != nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
			return
		}

		if rootLevel {
			_, err = sess.Window.SplitRoot(dir, newPane)
		} else {
			_, err = sess.Window.Split(dir, newPane)
		}
		if err != nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
			return
		}
		sess.mu.Unlock()

		sess.renderAndBroadcast()
		cc.Send(&Message{Type: MsgTypeCmdResult,
			CmdOutput: fmt.Sprintf("Split %s: new pane %s\n", dirName(dir), newPane.Meta.Name)})

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
		sess.Window.Focus(direction)
		sess.mu.Unlock()

		sess.renderAndBroadcast()

	case "output":
		if len(msg.CmdArgs) < 1 {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "usage: output <pane>"})
			return
		}
		sess.mu.Lock()
		if sess.Window == nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no session"})
			return
		}
		pane := sess.Window.ResolvePane(msg.CmdArgs[0])
		if pane == nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("pane %q not found", msg.CmdArgs[0])})
			return
		}
		out := pane.Output(50)
		sess.mu.Unlock()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: out + "\n"})

	case "spawn":
		// Parse: spawn --name NAME [--host HOST] [--task TASK]
		meta := mux.PaneMeta{Host: "local"}
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

		sess.mu.Lock()
		if sess.Window == nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no session"})
			return
		}

		initW, initH := sess.Window.Width, sess.Window.Height
		pane, err := sess.createPaneWithMeta(srv, meta, initW, render.PaneContentHeight(initH))
		if err != nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
			return
		}

		_, err = sess.Window.Split(mux.SplitHorizontal, pane)
		if err != nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
			return
		}
		sess.mu.Unlock()

		sess.renderAndBroadcast()
		cc.Send(&Message{Type: MsgTypeCmdResult,
			CmdOutput: fmt.Sprintf("Spawned %s in pane %d\n", meta.Name, pane.ID)})

	case "minimize":
		if len(msg.CmdArgs) < 1 {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "usage: minimize <pane>"})
			return
		}
		sess.mu.Lock()
		if sess.Window == nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no session"})
			return
		}
		pane := sess.Window.ResolvePane(msg.CmdArgs[0])
		if pane == nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("pane %q not found", msg.CmdArgs[0])})
			return
		}
		err := sess.Window.Minimize(pane.ID)
		sess.mu.Unlock()
		if err != nil {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
			return
		}
		sess.renderAndBroadcast()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: fmt.Sprintf("Minimized %s\n", pane.Meta.Name)})

	case "restore":
		if len(msg.CmdArgs) < 1 {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "usage: restore <pane>"})
			return
		}
		sess.mu.Lock()
		if sess.Window == nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no session"})
			return
		}
		pane := sess.Window.ResolvePane(msg.CmdArgs[0])
		if pane == nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("pane %q not found", msg.CmdArgs[0])})
			return
		}
		err := sess.Window.Restore(pane.ID)
		sess.mu.Unlock()
		if err != nil {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
			return
		}
		sess.renderAndBroadcast()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: fmt.Sprintf("Restored %s\n", pane.Meta.Name)})

	case "kill":
		if len(msg.CmdArgs) < 1 {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "usage: kill <pane>"})
			return
		}
		sess.mu.Lock()
		if sess.Window == nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no session"})
			return
		}
		pane := sess.Window.ResolvePane(msg.CmdArgs[0])
		if pane == nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("pane %q not found", msg.CmdArgs[0])})
			return
		}
		if len(sess.Panes) <= 1 {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "cannot kill last pane"})
			return
		}
		paneID := pane.ID
		paneName := pane.Meta.Name
		pane.Close()
		// Remove from pane list
		for i, p := range sess.Panes {
			if p.ID == paneID {
				sess.Panes = append(sess.Panes[:i], sess.Panes[i+1:]...)
				break
			}
		}
		sess.Window.ClosePane(paneID)
		sess.mu.Unlock()

		sess.renderAndBroadcast()
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
		sess.mu.Unlock()
		active := total - minimized
		cc.Send(&Message{Type: MsgTypeCmdResult,
			CmdOutput: fmt.Sprintf("panes: %d total, %d active, %d minimized\n", total, active, minimized)})

	default:
		cc.Send(&Message{Type: MsgTypeCmdResult,
			CmdErr: fmt.Sprintf("unknown command: %s", msg.CmdName)})
	}
}

func dirName(d mux.SplitDir) string {
	if d == mux.SplitHorizontal {
		return "horizontal"
	}
	return "vertical"
}
