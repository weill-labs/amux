package main

import (
	"encoding/json"
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
	"github.com/weill-labs/amux/internal/terminfo"
	"golang.org/x/sys/unix"
)

const defaultSessionName = "default"

const reconnectEventType = "reconnect"

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
	resolvedSessionName, args := resolveInvocationSession(os.Args[1:])
	runSessionCommand := func(cmdName string, cmdArgs []string) {
		runServerCommand(resolvedSessionName, cmdName, cmdArgs)
	}
	if os.Getenv("AMUX_CHECKPOINT") != "" {
		runServer(resolvedSessionName, false)
		return
	}

	if len(args) == 0 {
		if shouldAttemptTakeover() {
			if tryTakeover(resolvedSessionName) {
				return // takeover succeeded — managed mode started
			}
		}
		checkNesting(resolvedSessionName)
		if err := client.RunSession(resolvedSessionName); err != nil {
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

	case "install-terminfo":
		if err := terminfo.Install(); err != nil {
			fmt.Fprintf(os.Stderr, "amux install-terminfo: %v\n", err)
			os.Exit(1)
		}
		return

	case "_server":
		name := resolvedSessionName
		if len(args) > 1 {
			name = args[1]
		}
		runServer(name, false)

	case "attach":
		name, _ := parseAttachArgs(args[1:])
		if name == "" {
			name = resolvedSessionName
		}
		checkNesting(name)
		if err := client.RunSession(name); err != nil {
			fmt.Fprintf(os.Stderr, "amux: %v\n", err)
			os.Exit(1)
		}

	case "new":
		name := resolvedSessionName
		if len(args) > 1 {
			name = args[1]
		}
		checkNesting(name)
		if err := client.RunSession(name); err != nil {
			fmt.Fprintf(os.Stderr, "amux: %v\n", err)
			os.Exit(1)
		}

	case "split":
		splitArgs, err := parseSplitArgs(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "amux split: %v\n", err)
			fmt.Fprintf(os.Stderr, "usage: amux split [root] [--vertical|--horizontal] [--name NAME] [--host HOST] [--background]\n")
			os.Exit(1)
		}
		runSessionCommand("split", splitArgs)
	case "list":
		runSessionCommand("list", args[1:])
	case "status":
		runSessionCommand("status", nil)
	case "list-clients":
		runSessionCommand("list-clients", nil)
	case "connection-log":
		runSessionCommand("connection-log", nil)
	case "pane-log":
		runSessionCommand("pane-log", nil)
	case "capture":
		runSessionCommand("capture", args[1:])
	case "copy-mode":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux copy-mode <pane>\n")
			os.Exit(1)
		}
		runSessionCommand("copy-mode", []string{args[1]})
	case "zoom":
		runSessionCommand("zoom", args[1:])
	case "undo":
		runSessionCommand("undo", args[1:])
	case "swap":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux swap <pane1> <pane2> | swap forward | swap backward\n")
			os.Exit(1)
		}
		runSessionCommand("swap", args[1:])
	case "swap-tree":
		if len(args) != 3 {
			fmt.Fprintf(os.Stderr, "usage: amux swap-tree <pane1> <pane2>\n")
			os.Exit(1)
		}
		runSessionCommand("swap-tree", args[1:])
	case "move":
		if len(args) < 4 {
			fmt.Fprintf(os.Stderr, "usage: amux move <pane> --before <target> | move <pane> --after <target>\n")
			os.Exit(1)
		}
		runSessionCommand("move", args[1:])
	case "rotate":
		runSessionCommand("rotate", args[1:])
	case "resize-pane":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: amux resize-pane <pane> <direction> [delta]\n")
			os.Exit(1)
		}
		runSessionCommand("resize-pane", args[1:])
	case "minimize", "restore", "reset", "focus":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux %s <pane>\n", args[0])
			os.Exit(1)
		}
		runSessionCommand(args[0], []string{args[1]})
	case "kill":
		if err := server.ValidateKillCommandArgs(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", server.FormatKillCommandError(err, "amux"))
			os.Exit(1)
		}
		runSessionCommand("kill", args[1:])
	case "send-keys":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: amux send-keys <pane> [--wait-ready] [--continue-known-dialogs] [--hex] <keys>...\n")
			os.Exit(1)
		}
		runSessionCommand("send-keys", args[1:])
	case "broadcast":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux broadcast (--panes <pane,pane,...> | --window <index|name> | --match <glob>) [--hex] <keys>...\n")
			os.Exit(1)
		}
		runSessionCommand("broadcast", args[1:])
	case "type-keys":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux type-keys [--hex] <keys>...\n")
			os.Exit(1)
		}
		runSessionCommand("type-keys", args[1:])
	case "spawn":
		runSessionCommand("spawn", args[1:])
	case "new-window":
		runSessionCommand("new-window", args[1:])
	case "list-windows":
		runSessionCommand("list-windows", nil)
	case "select-window":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux select-window <index|name>\n")
			os.Exit(1)
		}
		runSessionCommand("select-window", []string{args[1]})
	case "next-window":
		runSessionCommand("next-window", nil)
	case "prev-window":
		runSessionCommand("prev-window", nil)
	case "rename-window":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux rename-window <name>\n")
			os.Exit(1)
		}
		runSessionCommand("rename-window", []string{args[1]})
	case "generation":
		runSessionCommand("generation", nil)
	case "ui-gen":
		runSessionCommand("ui-gen", args[1:])
	case "wait-layout":
		runSessionCommand("wait-layout", args[1:])
	case "wait-for":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: amux wait-for <pane> <substring> [--timeout <duration>]\n")
			os.Exit(1)
		}
		runSessionCommand("wait-for", args[1:])
	case "wait-ready":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: amux wait-ready <pane> [--timeout <duration>] [--continue-known-dialogs]")
			os.Exit(1)
		}
		runSessionCommand("wait-ready", args[1:])
	case "wait-vt-idle":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux wait-vt-idle <pane> [--settle <duration>] [--timeout <duration>]\n")
			os.Exit(1)
		}
		runSessionCommand("wait-vt-idle", args[1:])
	case "wait-idle":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux wait-idle <pane> [--timeout <duration>]\n")
			os.Exit(1)
		}
		runSessionCommand("wait-idle", args[1:])
	case "wait-busy":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux wait-busy <pane> [--timeout <duration>]\n")
			os.Exit(1)
		}
		runSessionCommand("wait-busy", args[1:])
	case "wait-ui":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux wait-ui <event> [--client <id>] [--after N] [--timeout <duration>]\n")
			os.Exit(1)
		}
		runSessionCommand("wait-ui", args[1:])
	case "hook-gen":
		runSessionCommand("hook-gen", nil)
	case "wait-hook":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux wait-hook <event> [--pane <pane>] [--after N] [--timeout <duration>]\n")
			os.Exit(1)
		}
		runSessionCommand("wait-hook", args[1:])
	case "clipboard-gen":
		runSessionCommand("clipboard-gen", nil)
	case "wait-clipboard":
		runSessionCommand("wait-clipboard", args[1:])
	case "resize-window":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: amux resize-window <cols> <rows>\n")
			os.Exit(1)
		}
		runSessionCommand("resize-window", args[1:])
	case "set-hook":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: amux set-hook <event> <command>\n")
			os.Exit(1)
		}
		runSessionCommand("set-hook", args[1:])
	case "unset-hook":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux unset-hook <event> [index]\n")
			os.Exit(1)
		}
		runSessionCommand("unset-hook", args[1:])
	case "list-hooks":
		runSessionCommand("list-hooks", nil)
	case "set-meta":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: amux set-meta <pane> key=value [key=value...]\n")
			os.Exit(1)
		}
		runSessionCommand("set-meta", args[1:])
	case "add-meta":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: amux add-meta <pane> key=value [key=value...]\n")
			os.Exit(1)
		}
		runSessionCommand("add-meta", args[1:])
	case "rm-meta":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: amux rm-meta <pane> key=value [key=value...]\n")
			os.Exit(1)
		}
		runSessionCommand("rm-meta", args[1:])

	case "events":
		runEventsCommand(resolvedSessionName, args[1:])
	case "hosts":
		runSessionCommand("hosts", nil)
	case "disconnect":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux disconnect <host>\n")
			os.Exit(1)
		}
		runSessionCommand("disconnect", []string{args[1]})
	case "reconnect":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux reconnect <host>\n")
			os.Exit(1)
		}
		runSessionCommand("reconnect", []string{args[1]})
	case "unsplice":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux unsplice <host>\n")
			os.Exit(1)
		}
		runSessionCommand("unsplice", []string{args[1]})
	case "reload-server":
		runSessionCommand("reload-server", nil)
	case "_inject-proxy":
		runSessionCommand("_inject-proxy", args[1:])
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

// resolveSessionName chooses the session for this invocation.
// Explicit -s wins, then AMUX_SESSION, then the default session.
func resolveSessionName(explicit string, explicitSet bool) string {
	if explicitSet {
		return explicit
	}
	if envSession := os.Getenv("AMUX_SESSION"); envSession != "" {
		return envSession
	}
	return defaultSessionName
}

func resolveInvocationSession(args []string) (string, []string) {
	explicit := defaultSessionName
	explicitSet := false
	for i := 0; i < len(args); i++ {
		if args[i] == "-s" && i+1 < len(args) {
			explicit = args[i+1]
			explicitSet = true
			return resolveSessionName(explicit, explicitSet), append(args[:i], args[i+2:]...)
		}
	}
	return resolveSessionName(explicit, explicitSet), args
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

// parseSplitArgs parses args for "amux split [root] [--vertical|--horizontal] [--name NAME] [--host HOST] [--background]".
// It normalizes back to the legacy server arg shape so keybindings and direct
// protocol callers can keep using "root" and "v".
func parseSplitArgs(args []string) ([]string, error) {
	rootLevel := false
	hostName := ""
	name := ""
	background := false
	dir := mux.SplitHorizontal
	hasExplicitDir := false

	setDir := func(next mux.SplitDir) error {
		if hasExplicitDir && dir != next {
			return fmt.Errorf("conflicting split directions")
		}
		dir = next
		hasExplicitDir = true
		return nil
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "root":
			rootLevel = true
		case "v", "--vertical":
			if err := setDir(mux.SplitVertical); err != nil {
				return nil, err
			}
		case "--horizontal":
			if err := setDir(mux.SplitHorizontal); err != nil {
				return nil, err
			}
		case "--host":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--host requires a value")
			}
			hostName = args[i+1]
			i++
		case "--name":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--name requires a value")
			}
			name = args[i+1]
			i++
		case "--background":
			background = true
		default:
			return nil, fmt.Errorf("unknown split arg %q", args[i])
		}
	}

	parsed := make([]string, 0, 4)
	if rootLevel {
		parsed = append(parsed, "root")
	}
	if dir == mux.SplitVertical {
		parsed = append(parsed, "v")
	}
	if hostName != "" {
		parsed = append(parsed, "--host", hostName)
	}
	if name != "" {
		parsed = append(parsed, "--name", name)
	}
	if background {
		parsed = append(parsed, "--background")
	}
	if len(parsed) == 0 {
		return nil, nil
	}
	return parsed, nil
}

func printUsage() {
	fmt.Println(`amux — Agent-Centric Terminal Multiplexer

Usage:
  amux [-s session]                    Start or attach to amux session
  amux [-s session] attach [session]   Attach to a session
  amux [-s session] new [name]         Start a new named session
  amux [-s session] list [--no-cwd]    List panes with metadata
  amux [-s session] status             Show pane/window summary
  amux [-s session] list-clients       List attached clients + client-local UI state
  amux [-s session] connection-log     Show recent client attach/detach history
  amux [-s session] pane-log           Show pane create/exit history with exit cwd/branch context
  amux [-s session] capture            Capture full composited screen
  amux [-s session] capture <pane>     Capture a single pane's output
  amux [-s session] capture --history <pane>
                                       Capture a pane's retained history + visible screen
  amux [-s session] capture --ansi     Capture with ANSI escape codes
  amux [-s session] capture --colors   Capture border color map
  amux [-s session] send-keys <pane> [--wait-ready] [--continue-known-dialogs] [--hex] <keys>...
                                       Send keystrokes to a pane
  amux [-s session] broadcast (--panes <pane,pane,...> | --window <index|name> | --match <glob>) [--hex] <keys>...
                                       Send the same keystrokes to multiple panes
  amux [-s session] type-keys [--hex] <keys>...
                                       Type keys through client input pipeline
  amux [-s session] spawn --name NAME [--host HOST] [--task TASK] [--color COLOR] [--background]
                                       Spawn a new agent pane
  amux [-s session] zoom [pane]        Toggle zoom (maximize) a pane
  amux [-s session] swap <p1> <p2>     Swap two panes by name or ID
  amux [-s session] swap-tree <p1> <p2>
                                       Swap the root-level groups containing two panes
  amux [-s session] move <pane> --before <target>
  amux [-s session] move <pane> --after <target>
                                       Move a pane's root-level group before or after another
  amux [-s session] rotate             Rotate pane positions forward
  amux [-s session] rotate --reverse   Rotate pane positions backward
  amux [-s session] minimize <pane>    Minimize a pane
  amux [-s session] restore <pane>     Restore a minimized pane
  amux [-s session] reset <pane>       Clear pane history and reset terminal state
  amux [-s session] resize-pane <pane> <dir> [n]
                                       Resize pane (dir: left/right/up/down)
  amux [-s session] kill <pane>        Kill a pane
  amux [-s session] undo              Undo last pane close
  amux [-s session] focus <pane>       Focus a pane by name or ID
  amux [-s session] copy-mode <pane>   Enter copy/scroll mode for a pane
  amux [-s session] set-meta <pane> key=value [key=value...]
                                       Set single-value pane metadata (task, branch, pr)
  amux [-s session] add-meta <pane> key=value [key=value...]
                                       Add pane metadata values (pr=NUMBER, issue=ID)
  amux [-s session] rm-meta <pane> key=value [key=value...]
                                       Remove pane metadata values (pr=NUMBER, issue=ID)
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
  amux [-s session] events [--filter type1,type2] [--pane <ref>] [--host <name>] [--client <id>] [--no-reconnect]
                                       Stream events as NDJSON (layout, output, idle, busy, vt-idle, hook, client-connect, client-disconnect, display-panes-*, choose-*, copy-mode-*, input-*, reconnect)
  amux [-s session] split [root] [--vertical|--horizontal] [--name NAME] [--host HOST] [--background]
                                       Split active pane (default: horizontal)
  amux [-s session] hosts              List configured remote hosts + status
  amux [-s session] disconnect <host>  Drop SSH connection to a host
  amux [-s session] reconnect <host>   Reconnect to a remote host
  amux [-s session] unsplice <host>    Revert SSH takeover for a host
  amux [-s session] reload-server      Hot-reload the server (preserves panes)
  amux [-s session] generation         Show current layout generation counter
  amux [-s session] ui-gen [--client <id>]
                                       Show current client UI generation counter
  amux [-s session] wait-layout [--after N] [--timeout 3s]
                                       Block until layout generation > N
  amux [-s session] wait-for <pane> <substring> [--timeout 3s]
                                       Block until substring appears in pane
  amux [-s session] wait-ready <pane> [--timeout 10s] [--continue-known-dialogs]
                                       Block until an agent pane reaches its input prompt
  amux [-s session] wait-vt-idle <pane> [--settle 2s] [--timeout 60s]
                                       Block until pane VT output quiesces
  amux [-s session] wait-busy <pane> [--timeout 5s]
                                       Block until pane has child processes
  amux [-s session] wait-idle <pane> [--timeout 5s]
                                       Block until pane becomes idle
  amux [-s session] wait-ui <event> [--client <id>] [--after N] [--timeout 5s]
                                       Block until a client-local UI state is reached
  amux [-s session] hook-gen          Show current hook completion generation
  amux [-s session] wait-hook <event> [--pane <ref>] [--after N] [--timeout 5s]
                                       Block until a matching hook completes
  amux install-terminfo                Install amux terminfo into ~/.terminfo
  amux version                         Show build version

Panes can be referenced by name (pane-1) or ID (1).

Inside an amux session (defaults, configurable via config.toml):
  Ctrl-a \                           Split active pane left/right
  Ctrl-a -                           Split active pane top/bottom
  Ctrl-a |                           Root-level split left/right
  Ctrl-a _                           Root-level split top/bottom
  Ctrl-a x                           Kill active pane
  Ctrl-a z                           Toggle zoom on active pane
  Ctrl-a M                           Toggle minimize/restore
  Ctrl-a q                           Show pane labels and jump to a pane
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
  Ctrl-a s                           Open window/pane chooser
  Ctrl-a w                           Open window chooser
  Ctrl-a q                           Show pane labels for quick jump
  Ctrl-a 1-9                         Select window by number
  Ctrl-a r                           Hot reload (re-exec binary)
  Ctrl-a d                           Detach from session
  Ctrl-a Ctrl-a                      Send literal Ctrl-a

Keybindings are configurable via ~/.config/amux/config.toml (or AMUX_CONFIG env var).
See https://github.com/weill-labs/amux for config format.`)
}

func openSignalFD(envVar, name string) *os.File {
	fdStr := os.Getenv(envVar)
	if fdStr == "" {
		return nil
	}
	os.Unsetenv(envVar)
	fd, err := strconv.Atoi(fdStr)
	if err != nil {
		return nil
	}
	return os.NewFile(uintptr(fd), name)
}

func writeSignalFD(f **os.File, msg string) {
	if *f == nil {
		return
	}
	if msg != "" {
		_, _ = (*f).Write([]byte(msg))
	}
	(*f).Close()
	*f = nil
}

// ---------------------------------------------------------------------------
// Built-in multiplexer: server daemon
// ---------------------------------------------------------------------------

func runServer(sessionName string, managedTakeover bool) {
	server.BuildVersion = buildVersion()

	if err := terminfo.Install(); err != nil {
		fmt.Fprintf(os.Stderr, "amux server: %v\n", err)
		os.Exit(1)
	}

	var s *server.Server
	var err error

	// Load config for remote host definitions
	cfg, cfgErr := config.Load(config.DefaultPath())
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "amux server: loading config: %v\n", cfgErr)
		cfg = &config.Config{Hosts: make(map[string]config.Host)}
	}
	scrollbackLines := cfg.EffectiveScrollbackLines()

	// Check for checkpoint restore (after server hot-reload)
	if cpPath := os.Getenv("AMUX_CHECKPOINT"); cpPath != "" {
		os.Unsetenv("AMUX_CHECKPOINT")
		cp, readErr := checkpoint.Read(cpPath)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "amux server: reading checkpoint: %v\n", readErr)
			os.Exit(1)
		}
		s, err = server.NewServerFromCheckpointWithScrollback(cp, scrollbackLines)
		if err != nil {
			fmt.Fprintf(os.Stderr, "amux server: restoring from checkpoint: %v\n", err)
			os.Exit(1)
		}
	} else if crashPath := server.DetectCrashedSession(sessionName); crashPath != "" {
		// Crash recovery: checkpoint exists but no server is running
		crashCP, readErr := checkpoint.ReadCrash(crashPath)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "amux server: unreadable crash checkpoint, starting fresh: %v\n", readErr)
			_ = checkpoint.RemoveCrashFile(crashPath) // remove stale checkpoint to avoid warning on every startup
			s, err = server.NewServerWithScrollback(sessionName, scrollbackLines)
			if err != nil {
				fmt.Fprintf(os.Stderr, "amux server: %v\n", err)
				os.Exit(1)
			}
		} else {
			fmt.Fprintf(os.Stderr, "amux server: recovering crashed session %q\n", sessionName)
			s, err = server.NewServerFromCrashCheckpointWithScrollback(sessionName, crashCP, crashPath, scrollbackLines)
			if err != nil {
				fmt.Fprintf(os.Stderr, "amux server: crash recovery: %v\n", err)
				os.Exit(1)
			}
		}
	} else {
		s, err = server.NewServerWithScrollback(sessionName, scrollbackLines)
		if err != nil {
			fmt.Fprintf(os.Stderr, "amux server: %v\n", err)
			os.Exit(1)
		}
	}

	// Read and unset server-only env vars so child processes don't inherit
	// them. Values are re-exported in Reload() before syscall.Exec.
	// Must be set before event loops can observe Env (e.g., exit-unattached).
	s.Env = server.ReadServerEnv()
	readySignal := openSignalFD("AMUX_READY_FD", "ready-signal")
	shutdownSignal := openSignalFD("AMUX_SHUTDOWN_FD", "shutdown-signal")
	defer writeSignalFD(&readySignal, "")
	defer writeSignalFD(&shutdownSignal, "")

	if managedTakeover {
		if err := s.EnsureInitialWindow(server.DefaultTermCols, server.DefaultTermRows); err != nil {
			fmt.Fprintf(os.Stderr, "amux server: initializing managed takeover session: %v\n", err)
			os.Exit(1)
		}
	}

	// Set up remote pane manager for all sessions
	s.SetupRemoteManager(cfg, server.BuildVersion)

	// Handle shutdown signals. The goroutine calls Shutdown() which closes
	// the listener, unblocking Run() below.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		s.Shutdown()
	}()

	triggerReload := make(chan struct{}, 1)
	execPath, execErr := reload.ResolveExecutable()
	if execErr == nil && !s.Env.NoWatch && reload.ShouldWatchBinary(execPath) {
		go reload.WatchBinary(execPath, triggerReload, nil)
		go func() {
			for range triggerReload {
				if reloadErr := s.Reload(execPath); reloadErr != nil {
					fmt.Fprintf(os.Stderr, "amux server: reload failed: %v\n", reloadErr)
				}
			}
		}()
	}

	runErr := make(chan error, 1)
	go func() {
		runErr <- s.Run()
	}()

	// Signal readiness after the accept loop starts so tests can attach
	// deterministically without racing startup.
	writeSignalFD(&readySignal, "ready\n")

	runResult := <-runErr
	s.Shutdown()
	writeSignalFD(&shutdownSignal, "shutdown\n")

	if runResult != nil {
		// listener closed is expected on shutdown
		if !strings.Contains(runResult.Error(), "use of closed") {
			fmt.Fprintf(os.Stderr, "amux server: %v\n", runResult)
			os.Exit(1)
		}
	}
}

// ---------------------------------------------------------------------------
// Server command client (for amux list, etc.)
// ---------------------------------------------------------------------------

// runStreamingCommand opens a persistent connection to the server and streams
// MsgTypeCmdResult messages to stdout until the connection closes.
// Used for long-lived commands like "events".
func runStreamingCommand(sessionName, cmdName string, args []string) {
	conn, err := connectStreamingCommand(sessionName, cmdName, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux %s: server not running (run 'amux' first)\n", cmdName)
		os.Exit(1)
	}
	streamCommandOutput(conn, cmdName)
}

type eventsClientOptions struct {
	reconnect      bool
	initialBackoff time.Duration
	maxBackoff     time.Duration
	maxRetries     int
}

func defaultEventsClientOptions() eventsClientOptions {
	return eventsClientOptions{
		reconnect:      true,
		initialBackoff: 1 * time.Second,
		maxBackoff:     30 * time.Second,
		maxRetries:     10,
	}
}

func parseEventsClientArgs(args []string) ([]string, eventsClientOptions) {
	opts := defaultEventsClientOptions()
	serverArgs := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--no-reconnect" {
			opts.reconnect = false
			continue
		}
		serverArgs = append(serverArgs, arg)
	}

	opts.initialBackoff = overrideDurationFromEnv("AMUX_EVENTS_RECONNECT_INITIAL_BACKOFF", opts.initialBackoff)
	opts.maxBackoff = overrideDurationFromEnv("AMUX_EVENTS_RECONNECT_MAX_BACKOFF", opts.maxBackoff)
	opts.maxRetries = overridePositiveIntFromEnv("AMUX_EVENTS_RECONNECT_MAX_RETRIES", opts.maxRetries)
	if opts.maxBackoff < opts.initialBackoff {
		opts.maxBackoff = opts.initialBackoff
	}
	return serverArgs, opts
}

func overrideDurationFromEnv(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func overridePositiveIntFromEnv(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func runEventsCommand(sessionName string, args []string) {
	serverArgs, opts := parseEventsClientArgs(args)

	conn, err := connectStreamingCommand(sessionName, "events", serverArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux events: server not running (run 'amux' first)\n")
		os.Exit(1)
	}

	for {
		err := streamCommandOutput(conn, "events")
		if !opts.reconnect {
			return
		}

		emitReconnectEvent()
		conn, err = reconnectStreamingCommand(sessionName, "events", serverArgs, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "amux events: reconnect failed after %d attempts: %v\n", opts.maxRetries, err)
			os.Exit(1)
		}
	}
}

func reconnectStreamingCommand(sessionName, cmdName string, args []string, opts eventsClientOptions) (net.Conn, error) {
	delay := opts.initialBackoff
	var lastErr error
	for attempt := 1; attempt <= opts.maxRetries; attempt++ {
		time.Sleep(delay)

		conn, err := connectStreamingCommand(sessionName, cmdName, args)
		if err == nil {
			return conn, nil
		}
		lastErr = err

		if delay < opts.maxBackoff {
			delay *= 2
			if delay > opts.maxBackoff {
				delay = opts.maxBackoff
			}
		}
	}
	return nil, lastErr
}

func emitReconnectEvent() {
	data, err := json.Marshal(server.Event{
		Type:      reconnectEventType,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return
	}
	fmt.Println(string(data))
}

func connectStreamingCommand(sessionName, cmdName string, args []string) (net.Conn, error) {
	sockPath := server.SocketPath(sessionName)
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, err
	}

	if err := server.WriteMsg(conn, newCommandMessage(cmdName, args)); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func streamCommandOutput(conn net.Conn, cmdName string) error {
	defer conn.Close()

	for {
		msg, err := server.ReadMsg(conn)
		if err != nil {
			return err // connection closed (server reload, shutdown, or pipe closed)
		}
		if msg.CmdErr != "" {
			fmt.Fprintf(os.Stderr, "amux %s: %s\n", cmdName, msg.CmdErr)
			os.Exit(1)
		}
		fmt.Print(msg.CmdOutput) // already newline-terminated
	}
}

func runServerCommand(sessionName, cmdName string, args []string) {
	sockPath := server.SocketPath(sessionName)
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux %s: server not running (run 'amux' first)\n", cmdName)
		os.Exit(1)
	}
	defer conn.Close()

	if cmdName == "reload-server" {
		args = prependReloadExecPathArg(reload.ResolveExecutable, args)
	}

	if err := server.WriteMsg(conn, newCommandMessage(cmdName, args)); err != nil {
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

func prependReloadExecPathArg(resolve func() (string, error), args []string) []string {
	execPath, err := resolve()
	if err != nil {
		return args
	}
	return append([]string{server.ReloadServerExecPathFlag, execPath}, args...)
}

func newCommandMessage(cmdName string, args []string) *server.Message {
	return &server.Message{
		Type:        server.MsgTypeCommand,
		CmdName:     cmdName,
		CmdArgs:     args,
		ActorPaneID: actorPaneIDFromEnv(),
	}
}

func actorPaneIDFromEnv() uint32 {
	raw := os.Getenv("AMUX_PANE")
	if raw == "" {
		return 0
	}
	id, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0
	}
	return uint32(id)
}

// checkNesting exits with an error if we're inside the same amux session
// we're trying to attach to (which would cause a frozen recursive nesting).
// Cross-session nesting is allowed. Users can override with `unset AMUX_SESSION`.
func checkNesting(target string) {
	if envSession := os.Getenv("AMUX_SESSION"); envSession == target {
		fmt.Fprintf(os.Stderr, "amux: cannot attach to session %q from inside itself (recursive nesting)\n", target)
		fmt.Fprintf(os.Stderr, "  unset AMUX_SESSION to override\n")
		os.Exit(1)
	}
}

// shouldAttemptTakeover reports whether the current process should try SSH
// takeover. Requires all three: in an SSH session, TERM=amux (forwarded via
// pty-req from an amux pane), and not already inside a remote amux pane.
func shouldAttemptTakeover() bool {
	return os.Getenv("SSH_CONNECTION") != "" && os.Getenv("TERM") == "amux" && os.Getenv("AMUX_PANE") == ""
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

	// Populate SSH connection info for return connection (deploy, bidirectional I/O).
	// SSH_CONNECTION format: "client_ip client_port server_ip server_port"
	if sshConn := os.Getenv("SSH_CONNECTION"); sshConn != "" {
		if parts := strings.Fields(sshConn); len(parts) >= 4 {
			req.SSHAddress = parts[2] + ":" + parts[3]
		}
	}
	if user := os.Getenv("USER"); user != "" {
		req.SSHUser = user
	}

	os.Stdout.Write(mux.FormatTakeoverSequence(req))

	session, ok := waitForTakeoverAck(os.Stdin, req.Session, 2*time.Second)
	if !ok {
		return false
	}
	fmt.Fprintf(os.Stderr, "amux: takeover acked, entering managed mode\n")
	runServer(session, true)
	return true
}

func waitForTakeoverAck(stdin *os.File, fallbackSession string, timeout time.Duration) (string, bool) {
	const maxTakeoverAckBuffer = 4 * 1024

	fd := int32(stdin.Fd())
	deadline := time.Now().Add(timeout)
	buf := make([]byte, 0, 256)
	tmp := make([]byte, 256)

	for {
		if session, ok := mux.FindTakeoverAck(buf, fallbackSession); ok {
			return session, true
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return "", false
		}

		timeoutMS := int((remaining + time.Millisecond - 1) / time.Millisecond)
		n, err := unix.Poll([]unix.PollFd{{Fd: fd, Events: unix.POLLIN}}, timeoutMS)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			return "", false
		}
		if n == 0 {
			return "", false
		}

		readN, readErr := stdin.Read(tmp)
		if readN > 0 {
			buf = append(buf, tmp[:readN]...)
			if len(buf) > maxTakeoverAckBuffer {
				buf = append(buf[:0], buf[len(buf)-maxTakeoverAckBuffer:]...)
			}
		}
		if readErr != nil {
			if session, ok := mux.FindTakeoverAck(buf, fallbackSession); ok {
				return session, true
			}
			return "", false
		}
	}
}
