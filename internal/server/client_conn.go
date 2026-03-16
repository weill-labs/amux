package server

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

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
			w := sess.ActiveWindow()
			if w != nil && w.ActivePane != nil {
				w.ActivePane.Write(msg.Input)
			}
			sess.mu.Unlock()

		case MsgTypeResize:
			sess.mu.Lock()
			// Resize all windows to match the terminal
			layoutH := msg.Rows - 1
			for _, w := range sess.Windows {
				w.Resize(msg.Cols, layoutH)
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

// handleCommand processes one-shot CLI commands (list, split, etc.).
func (cc *ClientConn) handleCommand(srv *Server, sess *Session, msg *Message) {
	switch msg.CmdName {
	case "list":
		sess.mu.Lock()
		var output string
		if len(sess.Panes) == 0 {
			output = "No panes.\n"
		} else {
			output = fmt.Sprintf("%-6s %-20s %-15s %-10s %s\n", "PANE", "NAME", "HOST", "WINDOW", "TASK")
			w := sess.ActiveWindow()
			for _, p := range sess.Panes {
				active := " "
				if w != nil && w.ActivePane != nil && w.ActivePane.ID == p.ID {
					active = "*"
				}
				winName := ""
				if pw := sess.FindWindowByPaneID(p.ID); pw != nil {
					winName = pw.Name
				}
				output += fmt.Sprintf("%-6s %-20s %-15s %-10s %s\n",
					fmt.Sprintf("%s%d", active, p.ID),
					p.Meta.Name, p.Meta.Host, winName, p.Meta.Task)
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
		w := sess.ActiveWindow()
		if w == nil {
			sess.mu.Unlock()
			return
		}

		switch direction {
		case "next", "left", "right", "up", "down":
			w.Focus(direction)
			name := w.ActivePane.Meta.Name
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: fmt.Sprintf("Focused %s\n", name)})
		default:
			// Treat as pane name or ID — reuse cross-window resolution
			pane := cc.resolvePaneAcrossWindows(sess, "focus", direction)
			if pane == nil {
				sess.mu.Unlock()
				return
			}
			// Switch to the pane's window and make it active
			if pw := sess.FindWindowByPaneID(pane.ID); pw != nil {
				sess.ActiveWindowID = pw.ID
				pw.FocusPane(pane)
			}
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: fmt.Sprintf("Focused %s\n", pane.Meta.Name)})
		}

		sess.broadcastLayout()

	case "capture":
		// amux capture [--ansi|--colors|--format json] [pane]
		includeANSI := false
		colorMap := false
		formatJSON := false
		var paneRef string
		for _, arg := range msg.CmdArgs {
			switch arg {
			case "--ansi":
				includeANSI = true
			case "--colors":
				colorMap = true
			case "--format":
				// next arg is the format value; handled below
			case "json":
				// only valid after --format; set flag
				formatJSON = true
			default:
				paneRef = arg
			}
		}

		// Three-way mutual exclusivity check
		flagCount := 0
		if includeANSI {
			flagCount++
		}
		if colorMap {
			flagCount++
		}
		if formatJSON {
			flagCount++
		}
		if flagCount > 1 {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "--ansi, --colors, and --format json are mutually exclusive"})
			return
		}

		if paneRef != "" {
			if colorMap {
				cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "--colors is only supported for full screen capture"})
				return
			}
			// Single pane capture
			sess.mu.Lock()
			pane := cc.resolvePaneAcrossWindows(sess, "capture", paneRef)
			if pane == nil {
				sess.mu.Unlock()
				return
			}
			var out string
			if formatJSON {
				out = sess.capturePaneJSON(pane)
			} else if includeANSI {
				out = pane.Render()
			} else {
				out = pane.Output(DefaultOutputLines)
			}
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: out + "\n"})
		} else {
			// Full composited screen capture
			var out string
			if formatJSON {
				out = sess.captureJSON() + "\n"
			} else if colorMap {
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
		w := sess.ActiveWindow()
		if w == nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no session"})
			return
		}
		// Resolve target pane: explicit arg or active pane
		var pane *mux.Pane
		if len(msg.CmdArgs) > 0 {
			pane = w.ResolvePane(msg.CmdArgs[0])
			if pane == nil {
				sess.mu.Unlock()
				cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("pane %q not found", msg.CmdArgs[0])})
				return
			}
		} else {
			pane = w.ActivePane
		}
		if pane == nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no active pane"})
			return
		}
		willUnzoom := w.ZoomedPaneID == pane.ID
		err := w.Zoom(pane.ID)
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
		cc.withPaneWindow(sess, "minimize", msg.CmdArgs, func(p *mux.Pane, w *mux.Window) (string, error) {
			return fmt.Sprintf("Minimized %s\n", p.Meta.Name), w.Minimize(p.ID)
		})

	case "restore":
		cc.withPaneWindow(sess, "restore", msg.CmdArgs, func(p *mux.Pane, w *mux.Window) (string, error) {
			return fmt.Sprintf("Restored %s\n", p.Meta.Name), w.Restore(p.ID)
		})

	case "toggle-minimize":
		sess.mu.Lock()
		w := sess.ActiveWindow()
		if w == nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no active window"})
			return
		}
		name, didMinimize, err := w.ToggleMinimize()
		sess.mu.Unlock()
		if err != nil {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
			return
		}
		sess.broadcastLayout()
		verb := "Restored"
		if didMinimize {
			verb = "Minimized"
		}
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: fmt.Sprintf("%s %s\n", verb, name)})

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
		// Remove from list BEFORE closing so onExit sees it's gone
		sess.removePane(paneID)
		sess.closePaneInWindow(paneID)
		sess.mu.Unlock()
		pane.Close()

		sess.broadcastLayout()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: fmt.Sprintf("Killed %s\n", paneName)})

	case "send-keys":
		if len(msg.CmdArgs) < 2 {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "usage: send-keys <pane> [--hex] <keys>..."})
			return
		}
		hexMode := false
		var keys []string
		for _, arg := range msg.CmdArgs[1:] {
			if arg == "--hex" {
				hexMode = true
			} else {
				keys = append(keys, arg)
			}
		}
		sess.mu.Lock()
		pane := cc.resolvePane(sess, "send-keys", msg.CmdArgs[:1])
		if pane == nil {
			sess.mu.Unlock()
			return
		}
		var data []byte
		if hexMode {
			for _, hexStr := range keys {
				b, err := hex.DecodeString(hexStr)
				if err != nil {
					sess.mu.Unlock()
					cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("invalid hex: %s", hexStr)})
					return
				}
				data = append(data, b...)
			}
		} else {
			for _, key := range keys {
				data = append(data, parseKey(key)...)
			}
		}
		pane.Write(data)
		sess.mu.Unlock()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: fmt.Sprintf("Sent %d bytes to %s\n", len(data), pane.Meta.Name)})

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
		w := sess.ActiveWindow()
		if w != nil && w.ZoomedPaneID != 0 {
			for _, p := range sess.Panes {
				if p.ID == w.ZoomedPaneID {
					zoomed = p.Meta.Name
					break
				}
			}
		}
		windowCount := len(sess.Windows)
		sess.mu.Unlock()
		active := total - minimized
		statusLine := fmt.Sprintf("windows: %d, panes: %d total, %d active, %d minimized", windowCount, total, active, minimized)
		if zoomed != "" {
			statusLine += fmt.Sprintf(", %s zoomed", zoomed)
		}
		if BuildVersion != "" {
			statusLine += fmt.Sprintf(", build: %s", BuildVersion)
		}
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: statusLine + "\n"})

	case "new-window":
		var name string
		for i := 0; i < len(msg.CmdArgs)-1; i += 2 {
			if msg.CmdArgs[i] == "--name" {
				name = msg.CmdArgs[i+1]
			}
		}
		cc.createNewWindow(srv, sess, name)

	case "list-windows":
		sess.mu.Lock()
		var output strings.Builder
		output.WriteString(fmt.Sprintf("%-6s %-20s %-8s\n", "WIN", "NAME", "PANES"))
		for i, w := range sess.Windows {
			active := " "
			if w.ID == sess.ActiveWindowID {
				active = "*"
			}
			output.WriteString(fmt.Sprintf("%-6s %-20s %-8d\n",
				fmt.Sprintf("%s%d:", active, i+1), w.Name, w.PaneCount()))
		}
		sess.mu.Unlock()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: output.String()})

	case "select-window":
		if len(msg.CmdArgs) < 1 {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "usage: select-window <index|name>"})
			return
		}
		ref := msg.CmdArgs[0]

		sess.mu.Lock()
		w := sess.ResolveWindow(ref)
		if w != nil {
			sess.ActiveWindowID = w.ID
		}
		sess.mu.Unlock()

		if w == nil {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("window %q not found", ref)})
			return
		}
		sess.broadcastLayout()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: "Switched window\n"})

	case "next-window":
		sess.mu.Lock()
		sess.NextWindow()
		sess.mu.Unlock()
		sess.broadcastLayout()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: "Next window\n"})

	case "prev-window":
		sess.mu.Lock()
		sess.PrevWindow()
		sess.mu.Unlock()
		sess.broadcastLayout()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: "Previous window\n"})

	case "rename-window":
		if len(msg.CmdArgs) < 1 {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "usage: rename-window <name>"})
			return
		}
		sess.mu.Lock()
		w := sess.ActiveWindow()
		if w == nil {
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "no window"})
			return
		}
		w.Name = msg.CmdArgs[0]
		sess.mu.Unlock()
		sess.broadcastLayout()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: fmt.Sprintf("Renamed window to %s\n", msg.CmdArgs[0])})

	case "resize-border":
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
		w := sess.ActiveWindow()
		if w != nil {
			w.ResizeBorder(x, y, delta)
		}
		sess.mu.Unlock()
		sess.broadcastLayout()

	case "resize-active":
		if len(msg.CmdArgs) < 2 {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "usage: resize-active <direction> <delta>"})
			return
		}
		direction := msg.CmdArgs[0]
		delta, err := strconv.Atoi(msg.CmdArgs[1])
		if err != nil {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "resize-active: invalid delta"})
			return
		}
		sess.mu.Lock()
		w := sess.ActiveWindow()
		if w != nil {
			w.ResizeActive(direction, delta)
		}
		sess.mu.Unlock()
		sess.broadcastLayout()
		cc.Send(&Message{Type: MsgTypeCmdResult})

	case "resize-window":
		if len(msg.CmdArgs) < 2 {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "usage: resize-window <cols> <rows>"})
			return
		}
		cols, err1 := strconv.Atoi(msg.CmdArgs[0])
		rows, err2 := strconv.Atoi(msg.CmdArgs[1])
		if err1 != nil || err2 != nil || cols <= 0 || rows <= 0 {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "resize-window: invalid dimensions"})
			return
		}
		sess.mu.Lock()
		layoutH := rows - 1
		for _, w := range sess.Windows {
			w.Resize(cols, layoutH)
		}
		sess.mu.Unlock()
		sess.broadcastLayout()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: fmt.Sprintf("Resized to %dx%d\n", cols, rows)})

	case "swap":
		sess.mu.Lock()
		w := sess.ActiveWindow()
		if w == nil {
			sess.mu.Unlock()
			return
		}

		var err error
		switch {
		case len(msg.CmdArgs) == 1 && msg.CmdArgs[0] == "forward":
			err = w.SwapPaneForward()
		case len(msg.CmdArgs) == 1 && msg.CmdArgs[0] == "backward":
			err = w.SwapPaneBackward()
		case len(msg.CmdArgs) == 2:
			pane1 := w.ResolvePane(msg.CmdArgs[0])
			if pane1 == nil {
				sess.mu.Unlock()
				cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("pane %q not found", msg.CmdArgs[0])})
				return
			}
			pane2 := w.ResolvePane(msg.CmdArgs[1])
			if pane2 == nil {
				sess.mu.Unlock()
				cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("pane %q not found", msg.CmdArgs[1])})
				return
			}
			err = w.SwapPanes(pane1.ID, pane2.ID)
		default:
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "usage: swap <pane1> <pane2> | swap forward | swap backward"})
			return
		}
		sess.mu.Unlock()

		if err != nil {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
			return
		}
		sess.broadcastLayout()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: "Swapped\n"})

	case "rotate":
		sess.mu.Lock()
		w := sess.ActiveWindow()
		if w == nil {
			sess.mu.Unlock()
			return
		}

		forward := true
		for _, arg := range msg.CmdArgs {
			if arg == "--reverse" {
				forward = false
			}
		}

		w.RotatePanes(forward)
		sess.mu.Unlock()

		sess.broadcastLayout()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: "Rotated\n"})

	case "copy-mode":
		sess.mu.Lock()
		pane := cc.resolvePane(sess, "copy-mode", msg.CmdArgs)
		if pane == nil {
			sess.mu.Unlock()
			return
		}
		paneID := pane.ID
		sess.mu.Unlock()
		sess.broadcast(&Message{Type: MsgTypeCopyMode, PaneID: paneID})
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: fmt.Sprintf("Copy mode entered for %s\n", pane.Meta.Name)})

	case "generation":
		gen := sess.generation.Load()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: fmt.Sprintf("%d\n", gen)})

	case "wait-layout":
		afterGen, timeout, err := parseWaitArgs(msg.CmdArgs)
		if err != nil {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
			return
		}
		gen, ok := sess.waitGeneration(afterGen, timeout)
		if !ok {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("timeout waiting for generation > %d (current: %d)", afterGen, gen)})
			return
		}
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: fmt.Sprintf("%d\n", gen)})

	case "clipboard-gen":
		gen := sess.clipboardGen.Load()
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: fmt.Sprintf("%d\n", gen)})

	case "wait-clipboard":
		afterGen, timeout, err := parseWaitArgs(msg.CmdArgs)
		if err != nil {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
			return
		}
		data, ok := sess.waitClipboard(afterGen, timeout)
		if !ok {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "timeout waiting for clipboard event"})
			return
		}
		cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: data + "\n"})

	case "wait-for":
		if len(msg.CmdArgs) < 2 {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "usage: wait-for <pane> <substring> [--timeout <duration>]"})
			return
		}
		paneRef := msg.CmdArgs[0]
		substr := msg.CmdArgs[1]
		timeout := 3 * time.Second
		for i := 2; i < len(msg.CmdArgs); i++ {
			if msg.CmdArgs[i] == "--timeout" && i+1 < len(msg.CmdArgs) {
				i++
				d, err := time.ParseDuration(msg.CmdArgs[i])
				if err != nil {
					cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("invalid timeout: %s", msg.CmdArgs[i])})
					return
				}
				timeout = d
			}
		}

		sess.mu.Lock()
		pane := cc.resolvePaneAcrossWindows(sess, "wait-for", paneRef)
		if pane == nil {
			sess.mu.Unlock()
			return
		}
		paneID := pane.ID
		sess.mu.Unlock()

		// Check immediately — the content may already be on screen.
		if sess.paneScreenContains(paneID, substr) {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: "matched\n"})
			return
		}

		// Subscribe to pane output notifications and poll on each write.
		ch := sess.subscribePaneOutput(paneID)
		defer sess.unsubscribePaneOutput(paneID, ch)

		timer := time.NewTimer(timeout)
		defer timer.Stop()
		for {
			select {
			case <-ch:
				if sess.paneScreenContains(paneID, substr) {
					cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: "matched\n"})
					return
				}
			case <-timer.C:
				cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("timeout waiting for %q in %s", substr, paneRef)})
				return
			}
		}

	case "wait-busy":
		if len(msg.CmdArgs) < 1 {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "usage: wait-busy <pane> [--timeout <duration>]"})
			return
		}
		paneRef := msg.CmdArgs[0]
		timeout := 5 * time.Second
		for i := 1; i < len(msg.CmdArgs); i++ {
			if msg.CmdArgs[i] == "--timeout" && i+1 < len(msg.CmdArgs) {
				i++
				d, err := time.ParseDuration(msg.CmdArgs[i])
				if err != nil {
					cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("invalid timeout: %s", msg.CmdArgs[i])})
					return
				}
				timeout = d
			}
		}

		sess.mu.Lock()
		pane := cc.resolvePaneAcrossWindows(sess, "wait-busy", paneRef)
		if pane == nil {
			sess.mu.Unlock()
			return
		}
		paneID := pane.ID
		sess.mu.Unlock()

		// Check immediately — the pane may already be busy.
		if sess.paneIsBusy(paneID) {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: "busy\n"})
			return
		}

		// Subscribe to pane output notifications and check on each write.
		// When the shell runs a command, it echoes the input (PTY output)
		// before forking the child, so output events are a reliable trigger.
		ch := sess.subscribePaneOutput(paneID)
		defer sess.unsubscribePaneOutput(paneID, ch)

		timer := time.NewTimer(timeout)
		defer timer.Stop()
		for {
			select {
			case <-ch:
				if sess.paneIsBusy(paneID) {
					cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: "busy\n"})
					return
				}
			case <-timer.C:
				cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: fmt.Sprintf("timeout waiting for %s to become busy", paneRef)})
				return
			}
		}

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
