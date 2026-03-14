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

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/server"

	"golang.org/x/term"
)

func main() {
	if len(os.Args) < 2 {
		// Default: start or attach to amux session
		if err := runMux("default"); err != nil {
			fmt.Fprintf(os.Stderr, "amux: %v\n", err)
			os.Exit(1)
		}
		return
	}

	switch os.Args[1] {
	// --- New built-in multiplexer commands ---
	case "_server":
		sessionName := "default"
		if len(os.Args) > 2 {
			sessionName = os.Args[2]
		}
		runServer(sessionName)

	case "attach":
		name, _ := parseAttachArgs(os.Args[2:])
		if name == "" {
			name = "default"
		}
		if err := runMux(name); err != nil {
			fmt.Fprintf(os.Stderr, "amux: %v\n", err)
			os.Exit(1)
		}

	case "new":
		name := "default"
		if len(os.Args) > 2 {
			name = os.Args[2]
		}
		if err := runMux(name); err != nil {
			fmt.Fprintf(os.Stderr, "amux: %v\n", err)
			os.Exit(1)
		}

	// --- Commands that talk to the server ---
	case "list":
		runServerCommand("list", nil)

	// --- Not yet migrated ---
	case "status":
		fmt.Fprintln(os.Stderr, "amux status: not yet migrated to built-in mux")
		os.Exit(1)
	case "output":
		fmt.Fprintln(os.Stderr, "amux output: not yet migrated to built-in mux")
		os.Exit(1)
	case "minimize":
		fmt.Fprintln(os.Stderr, "amux minimize: not yet migrated to built-in mux")
		os.Exit(1)
	case "restore":
		fmt.Fprintln(os.Stderr, "amux restore: not yet migrated to built-in mux")
		os.Exit(1)
	case "swap":
		fmt.Fprintln(os.Stderr, "amux swap: not yet migrated to built-in mux")
		os.Exit(1)
	case "spawn":
		fmt.Fprintln(os.Stderr, "amux spawn: not yet migrated to built-in mux")
		os.Exit(1)
	case "dashboard":
		fmt.Fprintln(os.Stderr, "amux dashboard: not yet migrated to built-in mux")
		os.Exit(1)

	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "amux: unknown command %q\n", os.Args[1])
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
	fmt.Println(`amux — Agent-Centric Terminal Multiplexer (built-in)

Usage:
  amux                              Start or attach to amux session
  amux attach [session]             Attach to a session
  amux new [name]                   Start a new named session
  amux list                         List panes with metadata

Inside an amux session:
  Ctrl-a d                          Detach from session
  Ctrl-a Ctrl-a                     Send literal Ctrl-a`)
}

func loadConfig() *config.Config {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux: warning: %v\n", err)
		return &config.Config{Hosts: make(map[string]config.Host)}
	}
	return cfg
}

// ---------------------------------------------------------------------------
// Built-in multiplexer: server daemon
// ---------------------------------------------------------------------------

func runServer(sessionName string) {
	s, err := server.NewServer(sessionName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux server: %v\n", err)
		os.Exit(1)
	}

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		s.Shutdown()
		os.Exit(0)
	}()

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
		cols = 80
	}
	if rows <= 0 {
		rows = 24
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

	// Enter raw mode
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	defer term.Restore(fd, oldState)

	// Forward SIGWINCH to server
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go func() {
		for range sigCh {
			c, r, _ := term.GetSize(fd)
			if c > 0 && r > 0 {
				server.WriteMsg(conn, &server.Message{
					Type: server.MsgTypeResize,
					Cols: c,
					Rows: r,
				})
			}
		}
	}()

	// Server → terminal: read rendered output, write to stdout
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			msg, err := server.ReadMsg(conn)
			if err != nil {
				return
			}
			switch msg.Type {
			case server.MsgTypeRender:
				os.Stdout.Write(msg.RenderData)
			case server.MsgTypeExit:
				return
			case server.MsgTypeBell:
				os.Stdout.Write([]byte{0x07})
			}
		}
	}()

	// Terminal → server: read input with Ctrl-a prefix handling
	go func() {
		buf := make([]byte, 4096)
		prefix := false

		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}

			var forward []byte
			for i := 0; i < n; i++ {
				if prefix {
					prefix = false
					switch buf[i] {
					case 'd':
						// Detach — flush any pending bytes, detach, exit
						if len(forward) > 0 {
							server.WriteMsg(conn, &server.Message{
								Type: server.MsgTypeInput, Input: forward,
							})
						}
						server.WriteMsg(conn, &server.Message{Type: server.MsgTypeDetach})
						conn.Close()
						return
					case 0x01:
						// Ctrl-a Ctrl-a → literal Ctrl-a
						forward = append(forward, 0x01)
					default:
						// Not a recognized command, forward prefix + byte
						forward = append(forward, 0x01, buf[i])
					}
					continue
				}

				if buf[i] == 0x01 {
					// Flush accumulated bytes before entering prefix mode
					if len(forward) > 0 {
						server.WriteMsg(conn, &server.Message{
							Type: server.MsgTypeInput, Input: forward,
						})
						forward = nil
					}
					prefix = true
					continue
				}

				forward = append(forward, buf[i])
			}

			if len(forward) > 0 {
				server.WriteMsg(conn, &server.Message{
					Type: server.MsgTypeInput, Input: forward,
				})
			}
		}
	}()

	<-done
	return nil
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
	sockPath := server.SocketPath("default")
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
