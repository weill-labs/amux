package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/mouse"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/render"
	"github.com/weill-labs/amux/internal/server"

	"golang.org/x/term"
)

// sessionName is the global session name, set by -s flag or defaulting to "default".
var sessionName = "default"

func main() {
	// Extract global -s flag before subcommand parsing
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		if args[i] == "-s" && i+1 < len(args) {
			sessionName = args[i+1]
			args = append(args[:i], args[i+2:]...)
			break
		}
	}

	if len(args) == 0 {
		if err := runMux(sessionName); err != nil {
			fmt.Fprintf(os.Stderr, "amux: %v\n", err)
			os.Exit(1)
		}
		return
	}

	switch args[0] {
	case "_server":
		name := sessionName
		if len(args) > 1 {
			name = args[1]
		}
		runServer(name)

	case "attach":
		name, _ := parseAttachArgs(args[1:])
		if name == "" {
			name = sessionName
		}
		if err := runMux(name); err != nil {
			fmt.Fprintf(os.Stderr, "amux: %v\n", err)
			os.Exit(1)
		}

	case "new":
		name := sessionName
		if len(args) > 1 {
			name = args[1]
		}
		if err := runMux(name); err != nil {
			fmt.Fprintf(os.Stderr, "amux: %v\n", err)
			os.Exit(1)
		}

	case "list":
		runServerCommand("list", nil)
	case "status":
		runServerCommand("status", nil)
	case "capture":
		runServerCommand("capture", args[1:])
	case "zoom":
		runServerCommand("zoom", args[1:])
	case "minimize", "restore", "kill", "focus":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux %s <pane>\n", args[0])
			os.Exit(1)
		}
		runServerCommand(args[0], []string{args[1]})
	case "spawn":
		runServerCommand("spawn", args[1:])
	case "reload-server":
		runServerCommand("reload-server", nil)
	case "dashboard":
		fmt.Fprintln(os.Stderr, "amux dashboard: not yet migrated to built-in mux")
		os.Exit(1)

	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "amux: unknown command %q\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

// parseAttachArgs parses args for "amux attach [-d] [session]".
func parseAttachArgs(args []string) (sessionName string, detachOthers bool) {
	for _, arg := range args {
		switch arg {
		case "-d":
			detachOthers = true
		default:
			sessionName = arg
		}
	}
	return
}

func printUsage() {
	fmt.Println(`amux — Agent-Centric Terminal Multiplexer

Usage:
  amux [-s session]                   Start or attach to amux session
  amux [-s session] attach [session]  Attach to a session
  amux [-s session] new [name]        Start a new named session
  amux [-s session] list              List panes with metadata
  amux [-s session] status            Show pane summary
  amux [-s session] capture           Capture full composited screen
  amux [-s session] capture <pane>    Capture a single pane's output
  amux [-s session] capture --ansi    Capture with ANSI escape codes
  amux [-s session] spawn --name NAME Spawn a new agent pane
  amux [-s session] zoom [pane]       Toggle zoom (maximize) a pane
  amux [-s session] minimize <pane>   Minimize a pane
  amux [-s session] restore <pane>    Restore a minimized pane
  amux [-s session] kill <pane>       Kill a pane
  amux [-s session] focus <pane>      Focus a pane by name or ID
  amux [-s session] reload-server     Hot-reload the server (preserves panes)

Panes can be referenced by name (pane-1) or ID (1).

Inside an amux session:
  Ctrl-a \                          Split active pane left/right
  Ctrl-a -                          Split active pane top/bottom
  Ctrl-a |                          Root-level split left/right
  Ctrl-a _                          Root-level split top/bottom
  Ctrl-a z                          Toggle zoom on active pane
  Ctrl-a o                          Cycle focus to next pane
  Ctrl-a h/j/k/l                    Focus left/down/up/right
  Ctrl-a r                          Hot reload (re-exec binary)
  Ctrl-a d                          Detach from session
  Ctrl-a Ctrl-a                     Send literal Ctrl-a`)
}

// ---------------------------------------------------------------------------
// Built-in multiplexer: server daemon
// ---------------------------------------------------------------------------

func runServer(sessionName string) {
	var s *server.Server
	var err error

	// Check for checkpoint restore (after server hot-reload)
	if cpPath := os.Getenv("AMUX_CHECKPOINT"); cpPath != "" {
		os.Unsetenv("AMUX_CHECKPOINT")
		cp, readErr := checkpoint.Read(cpPath)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "amux server: reading checkpoint: %v\n", readErr)
			os.Exit(1)
		}
		s, err = server.NewServerFromCheckpoint(cp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "amux server: restoring from checkpoint: %v\n", err)
			os.Exit(1)
		}
	} else {
		s, err = server.NewServer(sessionName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "amux server: %v\n", err)
			os.Exit(1)
		}
	}

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		s.Shutdown()
		os.Exit(0)
	}()

	// Server-side binary watcher for auto-reload
	triggerReload := make(chan struct{}, 1)
	execPath, execErr := resolveExecutable()
	if execErr == nil {
		go watchBinary(execPath, triggerReload)
		go func() {
			<-triggerReload
			if reloadErr := s.Reload(execPath); reloadErr != nil {
				fmt.Fprintf(os.Stderr, "amux server: reload failed: %v\n", reloadErr)
			}
		}()
	}

	if err := s.Run(); err != nil {
		// listener closed is expected on shutdown
		if !strings.Contains(err.Error(), "use of closed") {
			fmt.Fprintf(os.Stderr, "amux server: %v\n", err)
			os.Exit(1)
		}
	}
}

// ---------------------------------------------------------------------------
// Built-in multiplexer: client
// ---------------------------------------------------------------------------

// runMux connects to an existing server or starts one, then enters raw
// terminal mode for interactive use.
func runMux(sessionName string) error {
	sockPath := server.SocketPath(sessionName)

	// Start server daemon if no socket exists
	if !socketAlive(sockPath) {
		if err := startServerDaemon(sessionName); err != nil {
			return fmt.Errorf("starting server: %w", err)
		}
		// Wait for socket to appear
		if err := waitForSocket(sockPath, 5*time.Second); err != nil {
			return err
		}
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("connecting to server: %w", err)
	}
	defer conn.Close()

	fd := int(os.Stdin.Fd())
	cols, rows, _ := term.GetSize(fd)
	if cols <= 0 {
		cols = server.DefaultTermCols
	}
	if rows <= 0 {
		rows = server.DefaultTermRows
	}

	// Send attach
	if err := server.WriteMsg(conn, &server.Message{
		Type:    server.MsgTypeAttach,
		Session: sessionName,
		Cols:    cols,
		Rows:    rows,
	}); err != nil {
		return fmt.Errorf("sending attach: %w", err)
	}

	// Enter raw mode + alternate screen buffer
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	os.Stdout.Write([]byte(render.AltScreenEnter))
	os.Stdout.Write([]byte(render.MouseEnable))
	defer func() {
		os.Stdout.Write([]byte(render.MouseDisable))
		os.Stdout.Write([]byte(render.AltScreenExit))
		os.Stdout.Write([]byte(render.ResetTitle))
		term.Restore(fd, oldState)
	}()

	// Client-side renderer with per-pane emulators
	cr := NewClientRenderer(cols, rows)

	// Hot reload: resolve binary path once, start file watcher
	triggerReload := make(chan struct{}, 1)
	execPath, execErr := resolveExecutable()
	if execErr == nil {
		go watchBinary(execPath, triggerReload)
	}

	// Forward SIGWINCH to server and update client renderer
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go func() {
		for range sigCh {
			c, r, _ := term.GetSize(fd)
			if c > 0 && r > 0 {
				cr.Resize(c, r)
				server.WriteMsg(conn, &server.Message{
					Type: server.MsgTypeResize,
					Cols: c,
					Rows: r,
				})
			}
		}
	}()

	// Server → client renderer → stdout
	// Messages are dispatched to a coalescing render loop that caps at ~60fps.
	done := make(chan struct{})
	msgCh := make(chan *renderMsg, 256)

	// Read server messages and dispatch to render loop
	go func() {
		defer close(msgCh)
		for {
			msg, err := server.ReadMsg(conn)
			if err != nil {
				return
			}
			switch msg.Type {
			case server.MsgTypeLayout:
				msgCh <- &renderMsg{typ: renderMsgLayout, layout: msg.Layout}
			case server.MsgTypePaneOutput:
				msgCh <- &renderMsg{typ: renderMsgPaneOutput, paneID: msg.PaneID, data: msg.PaneData}
			case server.MsgTypeExit:
				msgCh <- &renderMsg{typ: renderMsgExit}
				return
			case server.MsgTypeBell:
				msgCh <- &renderMsg{typ: renderMsgBell}
			case server.MsgTypeServerReload:
				// Server is reloading — re-exec ourselves to reconnect
				select {
				case triggerReload <- struct{}{}:
				default:
				}
				return
			}
		}
	}()

	// Coalescing render loop
	go func() {
		defer close(done)
		cr.renderCoalesced(msgCh, func(data []byte) {
			os.Stdout.Write(data)
		})
	}()

	// Terminal → server: read input with mouse parsing + Ctrl-a prefix handling
	go func() {
		buf := make([]byte, 4096)
		prefix := false
		mouseParser := &mouse.Parser{}

		// Mouse drag state — caches border direction from initial press
		var drag dragState

		// processKeyByte handles a single non-mouse byte through the
		// Ctrl-a prefix system. Returns true if the goroutine should exit.
		processKeyByte := func(b byte, forward *[]byte) bool {
			if prefix {
				prefix = false
				switch b {
				case 'd':
					if len(*forward) > 0 {
						server.WriteMsg(conn, &server.Message{
							Type: server.MsgTypeInput, Input: *forward,
						})
					}
					server.WriteMsg(conn, &server.Message{Type: server.MsgTypeDetach})
					conn.Close()
					return true
				case '-':
					sendCommand(conn, "split", []string{"v"})
				case '\\':
					sendCommand(conn, "split", nil)
				case '|':
					sendCommand(conn, "split", []string{"root"})
				case '_':
					sendCommand(conn, "split", []string{"root", "v"})
				case 'o':
					sendCommand(conn, "focus", []string{"next"})
				case 'h':
					sendCommand(conn, "focus", []string{"left"})
				case 'l':
					sendCommand(conn, "focus", []string{"right"})
				case 'k':
					sendCommand(conn, "focus", []string{"up"})
				case 'j':
					sendCommand(conn, "focus", []string{"down"})
				case 'z':
					sendCommand(conn, "zoom", nil)
				case 'r':
					if len(*forward) > 0 {
						server.WriteMsg(conn, &server.Message{
							Type: server.MsgTypeInput, Input: *forward,
						})
						*forward = nil
					}
					select {
					case triggerReload <- struct{}{}:
					default:
					}
				case 0x01:
					*forward = append(*forward, 0x01)
				default:
					*forward = append(*forward, 0x01, b)
				}
				return false
			}

			if b == 0x01 {
				if len(*forward) > 0 {
					server.WriteMsg(conn, &server.Message{
						Type: server.MsgTypeInput, Input: *forward,
					})
					*forward = nil
				}
				prefix = true
				return false
			}

			*forward = append(*forward, b)
			return false
		}

		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}

			var forward []byte
			shouldExit := false

			for i := 0; i < n && !shouldExit; i++ {
				ev, isMouse, flushed := mouseParser.Feed(buf[i])

				if isMouse {
					// Flush any accumulated forward bytes before handling mouse
					if len(forward) > 0 {
						server.WriteMsg(conn, &server.Message{
							Type: server.MsgTypeInput, Input: forward,
						})
						forward = nil
					}
					handleMouseEvent(ev, cr, conn, &drag)
					continue
				}

				// Process flushed bytes (normal input that passed through parser)
				for _, fb := range flushed {
					if processKeyByte(fb, &forward) {
						shouldExit = true
						break
					}
				}
			}

			if shouldExit {
				return
			}

			if len(forward) > 0 {
				server.WriteMsg(conn, &server.Message{
					Type: server.MsgTypeInput, Input: forward,
				})
			}
		}
	}()

	// Wait for session end or hot reload trigger
	select {
	case <-done:
		return nil
	case <-triggerReload:
		if execPath != "" {
			execSelf(execPath, conn, fd, oldState)
		}
		// execSelf replaces the process; if we get here, exec failed fatally
		return nil
	}
}

// sendCommand sends a command to the server (non-blocking, ignores response).
func sendCommand(conn net.Conn, name string, args []string) {
	server.WriteMsg(conn, &server.Message{
		Type:    server.MsgTypeCommand,
		CmdName: name,
		CmdArgs: args,
	})
}

// handleMouseEvent dispatches a parsed mouse event to the appropriate action:
// click-to-focus, border drag, or scroll wheel.
func handleMouseEvent(ev mouse.Event, cr *ClientRenderer, conn net.Conn, drag *dragState) {
	cr.mu.Lock()
	layout := cr.layout
	cr.mu.Unlock()

	if layout == nil {
		return
	}

	switch {
	case ev.Action == mouse.Press && ev.Button == mouse.ButtonLeft:
		// Check if clicking on a border (start drag) or a pane (focus)
		if hit := layout.FindBorderAt(ev.X, ev.Y); hit != nil {
			drag.active = true
			drag.borderX = ev.X
			drag.borderY = ev.Y
			drag.borderDir = hit.Dir
		} else if cell := layout.FindLeafAt(ev.X, ev.Y); cell != nil {
			paneID := cell.CellPaneID()
			cr.mu.Lock()
			alreadyActive := paneID == cr.activePaneID
			cr.mu.Unlock()
			if !alreadyActive {
				sendCommand(conn, "focus", []string{fmt.Sprintf("%d", paneID)})
			}
		}

	case ev.Action == mouse.Motion && drag.active:
		dx := ev.X - ev.LastX
		dy := ev.Y - ev.LastY
		delta := dx
		if drag.borderDir == mux.SplitVertical {
			delta = dy
		}
		if delta != 0 {
			sendCommand(conn, "resize-border", []string{
				fmt.Sprintf("%d", drag.borderX),
				fmt.Sprintf("%d", drag.borderY),
				fmt.Sprintf("%d", delta),
			})
			if drag.borderDir == mux.SplitHorizontal {
				drag.borderX += dx
			} else {
				drag.borderY += dy
			}
		}

	case ev.Action == mouse.Release:
		drag.active = false

	case ev.Button == mouse.ScrollUp:
		// Scroll wheel sends arrow keys to the active pane
		server.WriteMsg(conn, &server.Message{
			Type: server.MsgTypeInput, Input: []byte("\033[A\033[A\033[A"),
		})
	case ev.Button == mouse.ScrollDown:
		server.WriteMsg(conn, &server.Message{
			Type: server.MsgTypeInput, Input: []byte("\033[B\033[B\033[B"),
		})
	}
}

// dragState tracks an in-progress border drag. The border direction is
// cached from the initial press so motion events don't need to re-query
// the layout (which may be stale during fast drags).
type dragState struct {
	active    bool
	borderX   int
	borderY   int
	borderDir mux.SplitDir
}

// startServerDaemon launches the server as a background daemon.
func startServerDaemon(sessionName string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	logDir := server.SocketDir()
	os.MkdirAll(logDir, 0700)
	logPath := filepath.Join(logDir, sessionName+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("opening log: %w", err)
	}

	cmd := exec.Command(exe, "_server", sessionName)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Detach from controlling terminal
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return err
	}
	logFile.Close()

	// Release the child process so it runs independently
	cmd.Process.Release()
	return nil
}

// socketAlive checks if a socket exists and a server is listening on it.
func socketAlive(sockPath string) bool {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// waitForSocket polls until the socket becomes available.
func waitForSocket(sockPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if socketAlive(sockPath) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("server did not start within %v", timeout)
}

// ---------------------------------------------------------------------------
// Server command client (for amux list, etc.)
// ---------------------------------------------------------------------------

func runServerCommand(cmdName string, args []string) {
	sockPath := server.SocketPath(sessionName)
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux %s: server not running (run 'amux' first)\n", cmdName)
		os.Exit(1)
	}
	defer conn.Close()

	if err := server.WriteMsg(conn, &server.Message{
		Type:    server.MsgTypeCommand,
		CmdName: cmdName,
		CmdArgs: args,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "amux %s: %v\n", cmdName, err)
		os.Exit(1)
	}

	reply, err := server.ReadMsg(conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux %s: %v\n", cmdName, err)
		os.Exit(1)
	}

	if reply.CmdErr != "" {
		fmt.Fprintf(os.Stderr, "amux %s: %s\n", cmdName, reply.CmdErr)
		os.Exit(1)
	}
	fmt.Print(reply.CmdOutput)
}
