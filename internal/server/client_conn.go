package server

import (
	"fmt"
	"net"
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
				sess.Window.Resize(msg.Cols, msg.Rows)
				sess.compositor.Resize(msg.Cols, msg.Rows)
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
		dir := mux.SplitHorizontal
		if len(msg.CmdArgs) > 0 && msg.CmdArgs[0] == "v" {
			dir = mux.SplitVertical
		}

		sess.mu.Lock()
		if sess.Window == nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no window"})
			return
		}

		// Create new pane sized to the active cell (will be resized by Split)
		cell := sess.Window.Root.FindPane(sess.Window.ActivePane.ID)
		if cell == nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "active pane not found"})
			return
		}

		newPane, err := sess.createPane(srv, cell.W, cell.H)
		if err != nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
			return
		}

		_, err = sess.Window.Split(dir, newPane)
		if err != nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
			return
		}
		sess.mu.Unlock()

		sess.renderAndBroadcast()
		cc.Send(&Message{Type: MsgTypeCmdResult,
			CmdOutput: fmt.Sprintf("Split %s: new pane %s\n", dirName(dir), newPane.Meta.Name)})

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
