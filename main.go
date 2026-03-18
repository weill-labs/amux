package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/client"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/reload"
	"github.com/weill-labs/amux/internal/server"
)

// sessionName is the global session name, set by -s flag or defaulting to "default".
var sessionName = "default"

// BuildCommit can be set via -ldflags "-X main.BuildCommit=abc1234".
// Falls back to VCS info from runtime/debug at startup.
var BuildCommit string

// buildVersion returns the build identifier (commit hash or "dev").
func buildVersion() string {
	if BuildCommit != "" {
		return BuildCommit
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				return s.Value[:7]
			}
		}
	}
	return "dev"
}

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
		// Nested detection: if running inside an SSH session (but not
		// inside a local amux pane on the same host), attempt takeover.
		if os.Getenv("SSH_CONNECTION") != "" && os.Getenv("AMUX_PANE") == "" {
			if tryTakeover(sessionName) {
				return // takeover succeeded — managed mode started
			}
		}
		if err := client.RunSession(sessionName); err != nil {
			fmt.Fprintf(os.Stderr, "amux: %v\n", err)
			os.Exit(1)
		}
		return
	}

	switch args[0] {
	case "version":
		if len(args) > 1 && args[1] == "--hash" {
			fmt.Println(buildVersion())
		} else {
			fmt.Printf("amux build: %s\n", buildVersion())
		}
		return

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
		if err := client.RunSession(name); err != nil {
			fmt.Fprintf(os.Stderr, "amux: %v\n", err)
			os.Exit(1)
		}

	case "new":
		name := sessionName
		if len(args) > 1 {
			name = args[1]
		}
		if err := client.RunSession(name); err != nil {
			fmt.Fprintf(os.Stderr, "amux: %v\n", err)
			os.Exit(1)
		}

	case "split":
		runServerCommand("split", args[1:])
	case "list":
		runServerCommand("list", nil)
	case "status":
		runServerCommand("status", nil)
	case "capture":
		runServerCommand("capture", args[1:])
	case "copy-mode":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux copy-mode <pane>\n")
			os.Exit(1)
		}
		runServerCommand("copy-mode", []string{args[1]})
	case "zoom":
		runServerCommand("zoom", args[1:])
	case "swap":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux swap <pane1> <pane2> | swap forward | swap backward\n")
			os.Exit(1)
		}
		runServerCommand("swap", args[1:])
	case "rotate":
		runServerCommand("rotate", args[1:])
	case "resize-pane":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: amux resize-pane <pane> <direction> [delta]\n")
			os.Exit(1)
		}
		runServerCommand("resize-pane", args[1:])
	case "minimize", "restore", "kill", "focus":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux %s <pane>\n", args[0])
			os.Exit(1)
		}
		runServerCommand(args[0], []string{args[1]})
	case "send-keys":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: amux send-keys <pane> <keys>...\n")
			os.Exit(1)
		}
		runServerCommand("send-keys", args[1:])
	case "type-keys":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux type-keys [--hex] <keys>...\n")
			os.Exit(1)
		}
		runServerCommand("type-keys", args[1:])
	case "spawn":
		runServerCommand("spawn", args[1:])
	case "new-window":
		runServerCommand("new-window", args[1:])
	case "list-windows":
		runServerCommand("list-windows", nil)
	case "select-window":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux select-window <index|name>\n")
			os.Exit(1)
		}
		runServerCommand("select-window", []string{args[1]})
	case "next-window":
		runServerCommand("next-window", nil)
	case "prev-window":
		runServerCommand("prev-window", nil)
	case "rename-window":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux rename-window <name>\n")
			os.Exit(1)
		}
		runServerCommand("rename-window", []string{args[1]})
	case "generation":
		runServerCommand("generation", nil)
	case "wait-layout":
		runServerCommand("wait-layout", args[1:])
	case "wait-for":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: amux wait-for <pane> <substring> [--timeout <duration>]\n")
			os.Exit(1)
		}
		runServerCommand("wait-for", args[1:])
	case "wait-idle":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux wait-idle <pane> [--timeout <duration>]\n")
			os.Exit(1)
		}
		runServerCommand("wait-idle", args[1:])
	case "wait-busy":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux wait-busy <pane> [--timeout <duration>]\n")
			os.Exit(1)
		}
		runServerCommand("wait-busy", args[1:])
	case "clipboard-gen":
		runServerCommand("clipboard-gen", nil)
	case "wait-clipboard":
		runServerCommand("wait-clipboard", args[1:])
	case "resize-window":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: amux resize-window <cols> <rows>\n")
			os.Exit(1)
		}
		runServerCommand("resize-window", args[1:])
	case "set-hook":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: amux set-hook <event> <command>\n")
			os.Exit(1)
		}
		runServerCommand("set-hook", args[1:])
	case "unset-hook":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux unset-hook <event> [index]\n")
			os.Exit(1)
		}
		runServerCommand("unset-hook", args[1:])
	case "list-hooks":
		runServerCommand("list-hooks", nil)

	case "events":
		runStreamingCommand("events", args[1:])
	case "hosts":
		runServerCommand("hosts", nil)
	case "disconnect":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux disconnect <host>\n")
			os.Exit(1)
		}
		runServerCommand("disconnect", []string{args[1]})
	case "reconnect":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux reconnect <host>\n")
			os.Exit(1)
		}
		runServerCommand("reconnect", []string{args[1]})
	case "unsplice":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux unsplice <host>\n")
			os.Exit(1)
		}
		runServerCommand("unsplice", []string{args[1]})
	case "reload-server":
		runServerCommand("reload-server", nil)
	case "_inject-proxy":
		runServerCommand("_inject-proxy", args[1:])
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
  amux [-s session]                    Start or attach to amux session
  amux [-s session] attach [session]   Attach to a session
  amux [-s session] new [name]         Start a new named session
  amux [-s session] list               List panes with metadata
  amux [-s session] status             Show pane/window summary
  amux [-s session] capture            Capture full composited screen
  amux [-s session] capture <pane>     Capture a single pane's output
  amux [-s session] capture --ansi     Capture with ANSI escape codes
  amux [-s session] capture --colors   Capture border color map
  amux [-s session] send-keys <pane> <keys>...
                                       Send keystrokes to a pane
  amux [-s session] type-keys [--hex] <keys>...
                                       Type keys through client input pipeline
  amux [-s session] spawn --name NAME  Spawn a new agent pane
  amux [-s session] zoom [pane]        Toggle zoom (maximize) a pane
  amux [-s session] swap <p1> <p2>     Swap two panes by name or ID
  amux [-s session] rotate             Rotate pane positions forward
  amux [-s session] rotate --reverse   Rotate pane positions backward
  amux [-s session] minimize <pane>    Minimize a pane
  amux [-s session] restore <pane>     Restore a minimized pane
  amux [-s session] resize-pane <pane> <dir> [n]
                                       Resize pane (dir: left/right/up/down)
  amux [-s session] kill <pane>        Kill a pane
  amux [-s session] focus <pane>       Focus a pane by name or ID
  amux [-s session] copy-mode <pane>   Enter copy/scroll mode for a pane
  amux [-s session] new-window         Create a new window
  amux [-s session] list-windows       List all windows
  amux [-s session] select-window <n>  Switch to window by index or name
  amux [-s session] next-window        Switch to next window
  amux [-s session] prev-window        Switch to previous window
  amux [-s session] rename-window <n>  Rename the active window
  amux [-s session] resize-window <c> <r>
                                       Resize window to cols x rows
  amux [-s session] set-hook <event> <command>
                                       Register a hook (events: on-idle, on-activity)
  amux [-s session] unset-hook <event> [index]
                                       Remove hook(s) for an event
  amux [-s session] list-hooks         List registered hooks
  amux [-s session] events [--filter type1,type2] [--pane <ref>] [--host <name>]
                                       Stream events as NDJSON (layout, output, idle, busy)
  amux [-s session] split --host HOST  Split with a remote pane on HOST
  amux [-s session] hosts              List configured remote hosts + status
  amux [-s session] disconnect <host>  Drop SSH connection to a host
  amux [-s session] reconnect <host>   Reconnect to a remote host
  amux [-s session] unsplice <host>    Revert SSH takeover for a host
  amux [-s session] reload-server      Hot-reload the server (preserves panes)
  amux [-s session] generation         Show current layout generation counter
  amux [-s session] wait-layout [--after N] [--timeout 3s]
                                       Block until layout generation > N
  amux [-s session] wait-for <pane> <substring> [--timeout 3s]
                                       Block until substring appears in pane
  amux [-s session] wait-busy <pane> [--timeout 5s]
                                       Block until pane has child processes
  amux [-s session] wait-idle <pane> [--timeout 5s]
                                       Block until pane becomes idle
  amux version                         Show build version

Panes can be referenced by name (pane-1) or ID (1).

Inside an amux session (defaults, configurable via config.toml):
  Ctrl-a \                           Split active pane left/right
  Ctrl-a -                           Split active pane top/bottom
  Ctrl-a |                           Root-level split left/right
  Ctrl-a _                           Root-level split top/bottom
  Ctrl-a x                           Kill active pane
  Ctrl-a z                           Toggle zoom on active pane
  Ctrl-a m                           Toggle minimize/restore
  Ctrl-a }                           Swap active pane with next
  Ctrl-a {                           Swap active pane with previous
  Ctrl-a o                           Cycle focus to next pane
  Ctrl-a h/j/k/l                     Focus left/down/up/right
  Ctrl-a arrow keys                  Focus in arrow direction
  Alt+h/j/k/l                        Focus left/down/up/right (no prefix)
  Ctrl-a H/J/K/L                     Resize pane left/down/up/right
  Ctrl-a [                           Enter copy/scroll mode
  Ctrl-a c                           Create new window
  Ctrl-a n                           Next window
  Ctrl-a p                           Previous window
  Ctrl-a 1-9                         Select window by number
  Ctrl-a r                           Hot reload (re-exec binary)
  Ctrl-a d                           Detach from session
  Ctrl-a Ctrl-a                      Send literal Ctrl-a

Keybindings are configurable via ~/.config/amux/config.toml (or AMUX_CONFIG env var).
See https://github.com/weill-labs/amux for config format.`)
}

// ---------------------------------------------------------------------------
// Built-in multiplexer: server daemon
// ---------------------------------------------------------------------------

func runServer(sessionName string) {
	server.BuildVersion = buildVersion()

	var s *server.Server
	var err error

	// Load config for remote host definitions
	cfg, cfgErr := config.Load(config.DefaultPath())
	if cfgErr != nil {
		cfg = &config.Config{Hosts: make(map[string]config.Host)}
	}

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
	} else if crashPath := server.DetectCrashedSession(sessionName); crashPath != "" {
		// Crash recovery: checkpoint exists but no server is running
		crashCP, readErr := checkpoint.ReadCrash(crashPath)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "amux server: reading crash checkpoint: %v\n", readErr)
			// Fall through to fresh start
			s, err = server.NewServer(sessionName)
		} else {
			fmt.Fprintf(os.Stderr, "amux server: recovering crashed session %q\n", sessionName)
			s, err = server.NewServerFromCrashCheckpoint(sessionName, crashCP)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "amux server: crash recovery: %v\n", err)
			os.Exit(1)
		}
	} else {
		s, err = server.NewServer(sessionName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "amux server: %v\n", err)
			os.Exit(1)
		}
	}

	// Set up remote pane manager for all sessions
	s.SetupRemoteManager(cfg)

	// Signal readiness on the fd specified by AMUX_READY_FD (used by
	// test harness for deterministic startup without polling).
	// Unset immediately so child processes (pane shells, inner amux)
	// don't inherit it and accidentally close an unrelated fd.
	if fdStr := os.Getenv("AMUX_READY_FD"); fdStr != "" {
		os.Unsetenv("AMUX_READY_FD")
		if fd, err := strconv.Atoi(fdStr); err == nil {
			if ready := os.NewFile(uintptr(fd), "ready-signal"); ready != nil {
				ready.Write([]byte("ready\n"))
				ready.Close()
			}
		}
	}

	// Handle shutdown signals. The goroutine calls Shutdown() which closes
	// the listener (unblocking Run()), then finishes cleanup (crash checkpoint
	// removal, pane teardown). shutdownDone lets the main goroutine wait for
	// cleanup to complete before exiting.
	sigCh := make(chan os.Signal, 1)
	shutdownDone := make(chan struct{})
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		s.Shutdown()
		close(shutdownDone)
	}()

	// Server-side binary watcher for auto-reload.
	// AMUX_NO_WATCH=1 disables watching (used by test harness for the outer
	// server so only the inner server responds to binary changes).
	// Unset immediately so child processes don't inherit it.
	noWatch := os.Getenv("AMUX_NO_WATCH") == "1"
	os.Unsetenv("AMUX_NO_WATCH")

	triggerReload := make(chan struct{}, 1)
	execPath, execErr := reload.ResolveExecutable()
	if execErr == nil && !noWatch {
		go reload.WatchBinary(execPath, triggerReload)
		go func() {
			for range triggerReload {
				if reloadErr := s.Reload(execPath); reloadErr != nil {
					fmt.Fprintf(os.Stderr, "amux server: reload failed: %v\n", reloadErr)
				}
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

	// Wait for Shutdown() to finish cleanup (crash checkpoint removal, etc.)
	// before the process exits.
	<-shutdownDone
}

// ---------------------------------------------------------------------------
// Server command client (for amux list, etc.)
// ---------------------------------------------------------------------------

// runStreamingCommand opens a persistent connection to the server and streams
// MsgTypeCmdResult messages to stdout until the connection closes.
// Used for long-lived commands like "events".
func runStreamingCommand(cmdName string, args []string) {
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

	for {
		msg, err := server.ReadMsg(conn)
		if err != nil {
			break // connection closed (server reload, shutdown, or pipe closed)
		}
		if msg.CmdErr != "" {
			fmt.Fprintf(os.Stderr, "amux %s: %s\n", cmdName, msg.CmdErr)
			os.Exit(1)
		}
		fmt.Print(msg.CmdOutput) // already newline-terminated
	}
}

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

// tryTakeover attempts an SSH session takeover. It emits a takeover sequence
// to stdout and waits up to 2 seconds for an ack from a local amux on stdin.
// If acked, it starts the server in managed mode (no TUI) and returns true.
// If no ack, returns false and the caller should proceed with normal startup.
func tryTakeover(sessionName string) bool {
	hostname, _ := os.Hostname()

	req := mux.TakeoverRequest{
		Session: sessionName + "@" + hostname,
		Host:    hostname,
		UID:     fmt.Sprintf("%d", os.Getuid()),
		Panes:   []mux.TakeoverPane{},
	}

	os.Stdout.Write(mux.FormatTakeoverSequence(req))

	type readResult struct {
		data []byte
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		buf := make([]byte, len(mux.TakeoverAck)+64)
		n, err := os.Stdin.Read(buf)
		ch <- readResult{data: buf[:n], err: err}
	}()

	select {
	case result := <-ch:
		if result.err != nil || len(result.data) == 0 {
			return false
		}
		if !strings.Contains(string(result.data), mux.TakeoverAck) {
			return false
		}
	case <-time.After(2 * time.Second):
		return false
	}

	fmt.Fprintf(os.Stderr, "amux: takeover acked, entering managed mode\n")
	runServer(req.Session)
	return true
}
