package cli

import (
	"fmt"
	"io"
	"os"
)

const (
	sendKeysUsage     = "usage: amux send-keys <pane> [--via pty|client] [--client <id>] [--wait ready|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>..."
	mouseUsage        = "usage: amux mouse [--client <id>] [--timeout <duration>] (press <x> <y> | motion <x> <y> | release <x> <y> | click <x> <y> | click <pane> [--status-line] | drag <pane> --to <pane>)"
	logUsage          = "usage: amux log <clients|panes>"
	debugUsage        = "usage: amux debug <goroutines|profile|heap|socket|frames|client-goroutines|client-profile|client-heap> [--duration <duration>]"
	leadUsage         = "usage: amux lead [pane] | amux lead --clear"
	metaUsage         = "usage: amux meta <set|get|rm> ..."
	moveUsage         = "usage: amux move <pane> up|down | amux move <pane> (--before <target>|--after <target>|--to-column <target>)"
	spawnUsage        = "usage: amux spawn [--auto] [--at <pane>] [--window <name|id>] [--vertical|--horizontal] [--root] [--focus] [--name NAME] [--host HOST] [--task TASK] [--color COLOR]"
	swapUsage         = "usage: amux swap <pane1> <pane2> [--tree] | amux swap forward | amux swap backward"
	cursorUsage       = "usage: amux cursor <layout|clipboard|ui> [--client <id>]"
	connectUsage      = "usage: amux connect <host>"
	disconnectUsage   = "usage: amux disconnect <host>"
	focusUsage        = "usage: amux focus <pane>"
	listClientsUsage  = "usage: amux list-clients"
	listUsage         = "usage: amux list [--no-cwd]"
	listWindowsUsage  = "usage: amux list-windows"
	reconnectUsage    = "usage: amux reconnect <host>"
	reloadServerUsage = "usage: amux reload-server"
	renameUsage       = "usage: amux rename <pane> <new-name>"
	renameWindowUsage = "usage: amux rename-window <name>"
	resetUsage        = "usage: amux reset <pane>"
	respawnUsage      = "usage: amux respawn <pane>"
	resizeWindowUsage = "usage: amux resize-window <cols> <rows>"
	rotateUsage       = "usage: amux rotate [--reverse]"
	selectWindowUsage = "usage: amux select-window <index|name>"
	statusUsage       = "usage: amux status"
	unspliceUsage     = "usage: amux unsplice <host>"
	undoUsage         = "usage: amux undo"
	waitUsage         = "usage: amux wait <idle|busy|exited|ready|content|layout|clipboard|checkpoint|ui> ..."
	newWindowUsage    = "usage: amux new-window [--name NAME]"
	nextWindowUsage   = "usage: amux next-window"
	prevWindowUsage   = "usage: amux prev-window"
	lastWindowUsage   = "usage: amux last-window"
	zoomUsage         = "usage: amux zoom [pane]"
)

var commandUsageByName = map[string]string{
	"_inject-proxy":    "usage: amux _inject-proxy <host>",
	"_layout-json":     "usage: amux _layout-json",
	"_server":          "usage: amux _server [session]",
	"broadcast":        "usage: amux broadcast (--panes <pane,pane,...> | --window <index|name> | --match <glob>) [--hex] <keys>...",
	"capture":          "usage: amux capture [pane] [--history <pane>] [--ansi] [--colors]",
	"copy-mode":        "usage: amux copy-mode [pane] [--wait ui=copy-mode-shown] [--timeout <duration>]",
	"cursor":           cursorUsage,
	"connect":          connectUsage,
	"debug":            debugUsage,
	"disconnect":       disconnectUsage,
	"equalize":         "usage: amux equalize [--vertical|--all]",
	"events":           "usage: amux events [--filter type1,type2] [--pane <ref>] [--host <name>] [--client <id>] [--no-reconnect]",
	"focus":            focusUsage,
	"hosts":            "usage: amux hosts",
	"install-terminfo": "usage: amux install-terminfo",
	"kill":             "usage: amux kill [--cleanup] [--timeout <duration>] [pane]",
	"lead":             leadUsage,
	"list":             listUsage,
	"list-clients":     listClientsUsage,
	"list-windows":     listWindowsUsage,
	"log":              logUsage,
	"meta":             metaUsage,
	"mouse":            mouseUsage,
	"move":             moveUsage,
	"new":              "usage: amux new [name]",
	"new-window":       newWindowUsage,
	"next-window":      nextWindowUsage,
	"prev-window":      prevWindowUsage,
	"last-window":      lastWindowUsage,
	"rename":           renameUsage,
	"remote":           remoteUsage,
	"reconnect":        reconnectUsage,
	"reload-server":    reloadServerUsage,
	"rename-window":    renameWindowUsage,
	"reset":            resetUsage,
	"respawn":          respawnUsage,
	"resize-pane":      "usage: amux resize-pane <pane> <direction> [delta]",
	"resize-window":    resizeWindowUsage,
	"rotate":           rotateUsage,
	"select-window":    selectWindowUsage,
	"send-keys":        sendKeysUsage,
	"spawn":            spawnUsage,
	"status":           statusUsage,
	"undo":             undoUsage,
	"swap":             swapUsage,
	"unsplice":         unspliceUsage,
	"version":          "usage: amux version [--hash|--json]",
	"wait":             waitUsage,
	"zoom":             zoomUsage,
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if isHelpFlag(arg) {
			return true
		}
	}
	return false
}

func isHelpFlag(arg string) bool {
	return arg == "--help" || arg == "-h"
}

func MaybePrintCommandHelp(stdout io.Writer, args []string) bool {
	if len(args) < 2 {
		return false
	}
	if !isHelpFlag(args[1]) {
		return false
	}
	usage, ok := commandUsageByName[args[0]]
	if !ok {
		return false
	}
	fmt.Fprintln(stdout, usage)
	return true
}

func MaybePrintKeyCommandUsage(stdout, stderr io.Writer, args []string, usage string, minArgs int) (handled bool, exitCode int) {
	if hasHelpFlag(args) {
		fmt.Fprintln(stdout, usage)
		return true, 0
	}
	if len(args) < minArgs {
		fmt.Fprintln(stderr, usage)
		return true, 1
	}
	return false, 0
}

func PrintUsage() {
	WriteUsage(os.Stdout)
}

func WriteUsage(w io.Writer) {
	fmt.Fprint(w, `amux — Agent-Centric Terminal Multiplexer

Usage:
  amux [-s session]                    Start or attach to amux session
  amux [-s session] new [name]         Start or attach to a named session
  amux [-s session] list [--no-cwd]    List panes with metadata
  amux [-s session] status             Show pane/window summary
  amux [-s session] list-clients       List attached clients + client-local UI state
  amux [-s session] log clients        Show recent client attach/detach history
  amux [-s session] log panes          Show pane create/exit history with exit cwd/branch context
  amux [-s session] debug goroutines   Print a live goroutine dump from the server pprof endpoint
  amux [-s session] debug profile [--duration 30s]
                                       Stream a CPU profile from the server pprof endpoint
  amux [-s session] debug heap         Print a live heap profile summary from the server pprof endpoint
  amux [-s session] debug socket       Print the Unix socket path for the server pprof endpoint
  amux [-s session] debug frames       Dump client render frame stats from the attached client
  amux [-s session] debug client-goroutines
                                       Print a live goroutine dump from the latest attached client pprof endpoint
  amux [-s session] debug client-profile [--duration 30s]
                                       Stream a CPU profile from the latest attached client pprof endpoint
  amux [-s session] debug client-heap  Print a live heap profile summary from the latest attached client pprof endpoint
  amux [-s session] capture            Capture full composited screen
  amux [-s session] capture --history --format json
                                       Capture full-session JSON with per-pane scrollback prepended to content
  amux [-s session] capture <pane>     Capture a single pane's output
  amux [-s session] capture --history <pane>
                                       Capture a pane's retained history + visible screen
  amux [-s session] capture --ansi     Capture with ANSI escape codes
  amux [-s session] capture --colors   Capture border color map
  amux [-s session] connect <host>     Connect to a remote amux session and mirror its panes locally
  amux [-s session] send-keys <pane> [--via pty|client] [--client <id>] [--wait ready|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>...
                                       Send keystrokes to a pane
  amux [-s session] mouse [--client <id>] [--timeout <duration>] ...
                                       Simulate mouse input through an attached client
  amux [-s session] broadcast (--panes <pane,pane,...> | --window <index|name> | --match <glob>) [--hex] <keys>...
                                       Send the same keystrokes to multiple panes
  amux [-s session] spawn [--auto] [--at <pane>] [--window <name|id>] [--vertical|--horizontal] [--root] [--focus] [--name NAME] [--host HOST] [--task TASK] [--color COLOR]
                                       Create a new pane using default spawn, column-fill auto spawn, or targeted split placement
  amux [-s session] zoom [pane]        Toggle zoom (maximize) a pane
  amux [-s session] swap <p1> <p2> [--tree]
                                       Swap two panes, or their root-level groups with --tree
  amux [-s session] swap forward|backward
                                       Swap the active pane with its next or previous sibling
  amux [-s session] move <pane> up|down
                                       Move a pane one slot within its split group
  amux [-s session] move <pane> --before <target>
  amux [-s session] move <pane> --after <target>
  amux [-s session] move <pane> --to-column <target>
                                       Reorder a pane relative to another pane or move it into another column
  amux [-s session] rotate             Rotate pane positions forward
  amux [-s session] rotate --reverse   Rotate pane positions backward
  amux [-s session] reset <pane>       Clear pane history and reset terminal state
  amux [-s session] respawn <pane>     Restart a pane shell in place
  amux [-s session] resize-pane <pane> <dir> [n]
                                       Resize pane (dir: left/right/up/down)
  amux [-s session] equalize [--vertical|--all]
                                       Rebalance root columns, column rows, or both
  amux [-s session] kill <pane>        Kill a pane
  amux [-s session] undo              Undo last pane close
  amux [-s session] focus <pane>       Focus a pane by name or ID
  amux [-s session] rename <pane> <n>  Rename a pane
  amux [-s session] lead [pane]
  amux [-s session] lead --clear
                                       Set or clear the lead pane
  amux [-s session] copy-mode [pane] [--wait ui=copy-mode-shown] [--timeout <duration>]
                                       Enter copy/scroll mode for a pane
  amux [-s session] meta set <pane> key=value [key=value...]
  amux [-s session] meta get <pane> [key]
  amux [-s session] meta rm <pane> key [key...]
                                       Manage generic pane metadata
  amux [-s session] new-window         Create a new window
  amux [-s session] list-windows       List all windows
  amux [-s session] select-window <n>  Switch to window by index or name
  amux [-s session] next-window        Switch to next window
  amux [-s session] prev-window        Switch to previous window
  amux [-s session] last-window        Switch to the previously active window
  amux [-s session] rename-window <n>  Rename the active window
  amux [-s session] resize-window <c> <r>
                                       Resize window to cols x rows
  amux [-s session] events [--filter type1,type2] [--pane <ref>] [--host <name>] [--client <id>] [--no-reconnect]
                                       Stream events as NDJSON (layout, output, idle, busy, exited, client-connect, client-disconnect, display-panes-*, choose-*, copy-mode-*, input-*, reconnect)
  amux [-s session] remote hosts       List configured remote hosts + status
  amux [-s session] remote disconnect <host>
                                       Drop a remote host connection
  amux [-s session] remote reconnect <host>
                                       Reconnect to a remote host
  amux [-s session] remote unsplice <host>
                                       Revert remote takeover for a host
  amux [-s session] remote reload-server
                                       Hot-reload the server (preserves panes)
  amux [-s session] cursor layout      Show current layout cursor
  amux [-s session] cursor clipboard   Show current clipboard cursor
  amux [-s session] cursor ui [--client <id>]
                                       Show current client UI cursor
  amux [-s session] wait layout [--after N] [--timeout 3s]
                                       Block until the next layout change after the cursor
  amux [-s session] wait clipboard [--after N] [--timeout 3s]
                                       Block until the next clipboard write after the cursor
  amux [-s session] wait content <pane> <substring> [--timeout 3s]
                                       Block until substring appears in pane
  amux [-s session] wait idle <pane> [--settle 2s] [--timeout 60s]
                                       Block until pane VT output quiesces
  amux [-s session] wait busy <pane> [--timeout 5s]
                                       Block until pane has a foreground process
  amux [-s session] wait ready <pane> [--timeout 10s]
                                       Block until pane VT output settles and no foreground process remains
  amux [-s session] wait exited <pane> [--timeout 5s]
                                       Block until pane has no foreground process
  amux [-s session] wait checkpoint [--after N] [--timeout 15s]
                                       Block until a crash checkpoint write completes
  amux [-s session] wait ui <event> [--client <id>] [--after N] [--timeout 5s]
                                       Block until a client-local UI state is reached
  amux install-terminfo                Install amux terminfo into ~/.terminfo
  amux version                         Show build version

Panes can be referenced by name (pane-1) or ID (1).

Inside an amux session:
  Ctrl-a \                           Root-level split left/right
  Ctrl-a -                           Split active pane top/bottom
  Ctrl-a |                           Split active pane left/right
  Ctrl-a _                           Root-level split top/bottom
  Ctrl-a a                           Spawn pane in column-fill order
  Ctrl-a x                           Kill active pane
  Ctrl-a z                           Toggle zoom on active pane
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
  Ctrl-a ;                           Last active window
  Ctrl-a s                           Open window/pane chooser
  Ctrl-a w                           Open window chooser
  Ctrl-a q                           Show pane labels for quick jump
  Ctrl-a 1-9                         Select window by number
  Ctrl-a r                           Hot reload (re-exec binary)
  Ctrl-a d                           Detach from session
  Ctrl-a Ctrl-a                      Send literal Ctrl-a

See https://github.com/weill-labs/amux for config format.`)
}
