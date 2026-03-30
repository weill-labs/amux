package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"golang.org/x/term"
)

const defaultSessionName = server.DefaultSessionName

const (
	sendKeysUsage     = "usage: amux send-keys <pane> [--via pty|client] [--wait ready|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>..."
	logUsage          = "usage: amux log <clients|panes>"
	leadUsage         = "usage: amux lead [pane] | amux lead --clear"
	metaUsage         = "usage: amux meta <set|get|rm> ..."
	moveUsage         = "usage: amux move <pane> up|down | amux move <pane> (--before <target>|--after <target>|--to-column <target>)"
	spawnUsage        = "usage: amux spawn [--at <pane>] [--vertical|--horizontal] [--root] [--spiral] [--focus] [--name NAME] [--host HOST] [--task TASK] [--color COLOR]"
	swapUsage         = "usage: amux swap <pane1> <pane2> [--tree] | amux swap forward | amux swap backward"
	cursorUsage       = "usage: amux cursor <layout|clipboard|ui> [--client <id>]"
	disconnectUsage   = "usage: amux disconnect <host>"
	focusUsage        = "usage: amux focus <pane>"
	listClientsUsage  = "usage: amux list-clients"
	listUsage         = "usage: amux list [--no-cwd]"
	listWindowsUsage  = "usage: amux list-windows"
	reconnectUsage    = "usage: amux reconnect <host>"
	reloadServerUsage = "usage: amux reload-server"
	renameWindowUsage = "usage: amux rename-window <name>"
	resetUsage        = "usage: amux reset <pane>"
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
	zoomUsage         = "usage: amux zoom [pane]"
)

const reconnectEventType = "reconnect"

type sessionCommandArgMode int

const (
	sessionCommandForwardArgs sessionCommandArgMode = iota
	sessionCommandNoArgs
	sessionCommandFirstArg
)

type sessionCommandSpec struct {
	connectName string
	minArgs     int
	usage       string
	argMode     sessionCommandArgMode
}

var canonicalSessionCommands = map[string]sessionCommandSpec{
	"_inject-proxy": {connectName: "_inject-proxy", argMode: sessionCommandForwardArgs},
	"_layout-json":  {connectName: "_layout-json", argMode: sessionCommandNoArgs},
	"capture":       {connectName: "capture", argMode: sessionCommandForwardArgs},
	"copy-mode":     {connectName: "copy-mode", argMode: sessionCommandForwardArgs},
	"cursor":        {connectName: "cursor", minArgs: 1, usage: cursorUsage, argMode: sessionCommandForwardArgs},
	"disconnect":    {connectName: "disconnect", minArgs: 1, usage: disconnectUsage, argMode: sessionCommandFirstArg},
	"focus":         {connectName: "focus", minArgs: 1, usage: focusUsage, argMode: sessionCommandFirstArg},
	"hosts":         {connectName: "hosts", argMode: sessionCommandNoArgs},
	"list":          {connectName: "list", usage: listUsage, argMode: sessionCommandForwardArgs},
	"list-clients":  {connectName: "list-clients", usage: listClientsUsage, argMode: sessionCommandNoArgs},
	"list-windows":  {connectName: "list-windows", usage: listWindowsUsage, argMode: sessionCommandNoArgs},
	"new-window":    {connectName: "new-window", usage: newWindowUsage, argMode: sessionCommandForwardArgs},
	"next-window":   {connectName: "next-window", usage: nextWindowUsage, argMode: sessionCommandNoArgs},
	"prev-window":   {connectName: "prev-window", usage: prevWindowUsage, argMode: sessionCommandNoArgs},
	"reconnect":     {connectName: "reconnect", minArgs: 1, usage: reconnectUsage, argMode: sessionCommandFirstArg},
	"reload-server": {connectName: "reload-server", usage: reloadServerUsage, argMode: sessionCommandNoArgs},
	"rename-window": {connectName: "rename-window", minArgs: 1, usage: renameWindowUsage, argMode: sessionCommandFirstArg},
	"reset":         {connectName: "reset", minArgs: 1, usage: resetUsage, argMode: sessionCommandFirstArg},
	"resize-window": {connectName: "resize-window", minArgs: 2, usage: resizeWindowUsage, argMode: sessionCommandForwardArgs},
	"rotate":        {connectName: "rotate", usage: rotateUsage, argMode: sessionCommandForwardArgs},
	"select-window": {connectName: "select-window", minArgs: 1, usage: selectWindowUsage, argMode: sessionCommandFirstArg},
	"status":        {connectName: "status", usage: statusUsage, argMode: sessionCommandNoArgs},
	"undo":          {connectName: "undo", usage: undoUsage, argMode: sessionCommandNoArgs},
	"unsplice":      {connectName: "unsplice", minArgs: 1, usage: unspliceUsage, argMode: sessionCommandFirstArg},
	"wait":          {connectName: "wait", minArgs: 1, usage: waitUsage, argMode: sessionCommandForwardArgs},
	"zoom":          {connectName: "zoom", usage: zoomUsage, argMode: sessionCommandForwardArgs},
}

var commandUsageByName = map[string]string{
	"_inject-proxy":    "usage: amux _inject-proxy <host>",
	"_layout-json":     "usage: amux _layout-json",
	"_server":          "usage: amux _server [session]",
	"broadcast":        "usage: amux broadcast (--panes <pane,pane,...> | --window <index|name> | --match <glob>) [--hex] <keys>...",
	"capture":          "usage: amux capture [pane] [--history <pane>] [--ansi] [--colors]",
	"copy-mode":        "usage: amux copy-mode [pane] [--wait ui=copy-mode-shown] [--timeout <duration>]",
	"cursor":           "usage: amux cursor <layout|clipboard|ui> [--client <id>]",
	"disconnect":       disconnectUsage,
	"equalize":         "usage: amux equalize [--vertical|--all]",
	"events":           "usage: amux events [--filter type1,type2] [--pane <ref>] [--host <name>] [--client <id>] [--no-reconnect]",
	"focus":            "usage: amux focus <pane>",
	"hosts":            "usage: amux hosts",
	"install-terminfo": "usage: amux install-terminfo",
	"kill":             "usage: amux kill [--cleanup] [--timeout <duration>] [pane]",
	"lead":             leadUsage,
	"list":             listUsage,
	"list-clients":     listClientsUsage,
	"list-windows":     listWindowsUsage,
	"log":              logUsage,
	"meta":             metaUsage,
	"move":             moveUsage,
	"new":              "usage: amux new [name]",
	"new-window":       newWindowUsage,
	"next-window":      nextWindowUsage,
	"prev-window":      prevWindowUsage,
	"reconnect":        reconnectUsage,
	"reload-server":    reloadServerUsage,
	"rename-window":    renameWindowUsage,
	"reset":            "usage: amux reset <pane>",
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
	"wait":             "usage: amux wait <idle|busy|exited|ready|content|layout|clipboard|checkpoint|ui> ...",
	"zoom":             zoomUsage,
}

// BuildCommit can be set via -ldflags "-X main.BuildCommit=abc1234".
// Falls back to VCS info from runtime/debug at startup.
var BuildCommit string

type versionInfo struct {
	Build             string `json:"build"`
	CheckpointVersion int    `json:"checkpoint_version"`
}

func (v versionInfo) String() string {
	return fmt.Sprintf("%s (checkpoint v%d)", v.Build, v.CheckpointVersion)
}

func currentVersionInfo() versionInfo {
	return versionInfo{
		Build:             buildHash(),
		CheckpointVersion: checkpoint.ServerCheckpointVersion,
	}
}

// buildHash returns the build identifier (commit hash or "dev").
func buildHash() string {
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

func buildVersion() string {
	return currentVersionInfo().String()
}

func writeVersionOutput(w io.Writer, args []string) error {
	switch len(args) {
	case 0:
		_, err := fmt.Fprintf(w, "amux build: %s\n", buildVersion())
		return err
	case 1:
		switch args[0] {
		case "--hash":
			_, err := fmt.Fprintln(w, buildHash())
			return err
		case "--json":
			return json.NewEncoder(w).Encode(currentVersionInfo())
		}
	}
	return fmt.Errorf("usage: amux version [--hash|--json]")
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
		if err := client.RunSession(resolvedSessionName, term.GetSize); err != nil {
			fmt.Fprintf(os.Stderr, "amux: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if maybePrintCommandHelp(os.Stdout, args) {
		return
	}
	if cmdName, cmdArgs, handled, err := resolveCanonicalSessionCommand(args); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		runSessionCommand(cmdName, cmdArgs)
		return
	}

	switch args[0] {
	case "version":
		if err := writeVersionOutput(os.Stdout, args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
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

	case "new":
		name := resolvedSessionName
		if len(args) > 1 {
			name = args[1]
		}
		checkNesting(name)
		if err := client.RunSession(name, term.GetSize); err != nil {
			fmt.Fprintf(os.Stderr, "amux: %v\n", err)
			os.Exit(1)
		}

	case "log":
		cmdName, cmdArgs, err := parseLogArgs(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, logUsage)
			os.Exit(1)
		}
		runSessionCommand(cmdName, cmdArgs)
	case "swap":
		cmdName, cmdArgs, err := parseSwapArgs(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, swapUsage)
			os.Exit(1)
		}
		runSessionCommand(cmdName, cmdArgs)
	case "move":
		cmdName, cmdArgs, err := parseMoveArgs(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, moveUsage)
			os.Exit(1)
		}
		runSessionCommand(cmdName, cmdArgs)
	case "resize-pane":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: amux resize-pane <pane> <direction> [delta]\n")
			os.Exit(1)
		}
		runSessionCommand("resize-pane", args[1:])
	case "equalize":
		equalizeArgs, err := parseEqualizeArgs(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "amux equalize: %v\n", err)
			fmt.Fprintf(os.Stderr, "usage: amux equalize [--vertical|--all]\n")
			os.Exit(1)
		}
		runSessionCommand("equalize", equalizeArgs)
	case "kill":
		if err := server.ValidateKillCommandArgs(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", server.FormatKillCommandError(err, "amux"))
			os.Exit(1)
		}
		runSessionCommand("kill", args[1:])
	case "send-keys":
		if handled, exitCode := maybePrintKeyCommandUsage(os.Stdout, os.Stderr, args[1:], sendKeysUsage, 2); handled {
			if exitCode != 0 {
				os.Exit(exitCode)
			}
			return
		}
		runSessionCommand("send-keys", args[1:])
	case "broadcast":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: amux broadcast (--panes <pane,pane,...> | --window <index|name> | --match <glob>) [--hex] <keys>...\n")
			os.Exit(1)
		}
		runSessionCommand("broadcast", args[1:])
	case "spawn":
		cmdName, cmdArgs, err := parseSpawnCommandArgs(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, spawnUsage)
			os.Exit(1)
		}
		runSessionCommand(cmdName, cmdArgs)
	case "lead":
		cmdName, cmdArgs, err := parseLeadArgs(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, leadUsage)
			os.Exit(1)
		}
		runSessionCommand(cmdName, cmdArgs)
	case "meta":
		if err := validateMetaArgs(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		runSessionCommand("meta", args[1:])

	case "events":
		runEventsCommand(resolvedSessionName, args[1:])

	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "amux: unknown command %q\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

// resolveSessionName chooses the session for this invocation.
// Explicit -s wins, then AMUX_SESSION, then the implicit main session.
func resolveSessionName(explicit string, explicitSet bool) string {
	if explicitSet {
		return explicit
	}
	if envSession := os.Getenv("AMUX_SESSION"); envSession != "" {
		return envSession
	}
	return defaultSessionName
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

func maybePrintCommandHelp(stdout io.Writer, args []string) bool {
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

func maybePrintKeyCommandUsage(stdout, stderr io.Writer, args []string, usage string, minArgs int) (handled bool, exitCode int) {
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

func resolveCanonicalSessionCommand(args []string) (cmdName string, cmdArgs []string, handled bool, err error) {
	if len(args) == 0 {
		return "", nil, false, nil
	}

	spec, ok := canonicalSessionCommands[args[0]]
	if !ok {
		return "", nil, false, nil
	}
	if len(args)-1 < spec.minArgs {
		return "", nil, true, errors.New(spec.usage)
	}

	switch spec.argMode {
	case sessionCommandNoArgs:
		return spec.connectName, nil, true, nil
	case sessionCommandFirstArg:
		return spec.connectName, []string{args[1]}, true, nil
	default:
		return spec.connectName, args[1:], true, nil
	}
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

func parseLogArgs(args []string) (string, []string, error) {
	if len(args) != 1 {
		return "", nil, fmt.Errorf(logUsage)
	}
	switch args[0] {
	case "clients":
		return "connection-log", nil, nil
	case "panes":
		return "pane-log", nil, nil
	default:
		return "", nil, fmt.Errorf(logUsage)
	}
}

func parseLeadArgs(args []string) (string, []string, error) {
	switch {
	case len(args) == 0:
		return "set-lead", nil, nil
	case len(args) == 1 && args[0] == "--clear":
		return "unset-lead", nil, nil
	case len(args) == 1 && !strings.HasPrefix(args[0], "-"):
		return "set-lead", []string{args[0]}, nil
	default:
		return "", nil, fmt.Errorf(leadUsage)
	}
}

func validateMetaArgs(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(metaUsage)
	}
	switch args[0] {
	case "set":
		if len(args) < 3 {
			return fmt.Errorf("usage: amux meta set <pane> key=value [key=value...]")
		}
	case "get":
		if len(args) < 2 || len(args) > 3 {
			return fmt.Errorf("usage: amux meta get <pane> [key]")
		}
	case "rm":
		if len(args) < 3 {
			return fmt.Errorf("usage: amux meta rm <pane> key [key...]")
		}
	default:
		return fmt.Errorf(metaUsage)
	}
	return nil
}

func parseSwapArgs(args []string) (string, []string, error) {
	if len(args) == 0 {
		return "", nil, fmt.Errorf(swapUsage)
	}
	filtered := make([]string, 0, len(args))
	tree := false
	for _, arg := range args {
		if arg == "--tree" {
			if tree {
				return "", nil, fmt.Errorf(swapUsage)
			}
			tree = true
			continue
		}
		filtered = append(filtered, arg)
	}
	if tree {
		if len(filtered) != 2 {
			return "", nil, fmt.Errorf(swapUsage)
		}
		return "swap-tree", filtered, nil
	}
	if len(filtered) == 1 && (filtered[0] == "forward" || filtered[0] == "backward") {
		return "swap", filtered, nil
	}
	if len(filtered) == 2 {
		return "swap", filtered, nil
	}
	return "", nil, fmt.Errorf(swapUsage)
}

func parseMoveArgs(args []string) (string, []string, error) {
	if len(args) < 2 {
		return "", nil, fmt.Errorf(moveUsage)
	}
	paneRef := args[0]
	switch {
	case len(args) == 2 && args[1] == "up":
		return "move-up", []string{paneRef}, nil
	case len(args) == 2 && args[1] == "down":
		return "move-down", []string{paneRef}, nil
	case len(args) == 3 && (args[1] == "--before" || args[1] == "--after"):
		return "move", []string{paneRef, args[1], args[2]}, nil
	case len(args) == 3 && args[1] == "--to-column":
		return "move-to", []string{paneRef, args[2]}, nil
	default:
		return "", nil, fmt.Errorf(moveUsage)
	}
}

type spawnCLIOptions struct {
	at             string
	dir            mux.SplitDir
	hasExplicitDir bool
	root           bool
	spiral         bool
	focus          bool
	name           string
	host           string
	task           string
	color          string
}

func parseSpawnCommandArgs(args []string) (string, []string, error) {
	opts := spawnCLIOptions{dir: mux.SplitHorizontal}

	setDir := func(next mux.SplitDir) error {
		if opts.hasExplicitDir && opts.dir != next {
			return fmt.Errorf(spawnUsage)
		}
		opts.dir = next
		opts.hasExplicitDir = true
		return nil
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--at":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf(spawnUsage)
			}
			opts.at = args[i+1]
			i++
		case "--vertical":
			if err := setDir(mux.SplitVertical); err != nil {
				return "", nil, err
			}
		case "--horizontal":
			if err := setDir(mux.SplitHorizontal); err != nil {
				return "", nil, err
			}
		case "--root":
			opts.root = true
		case "--spiral":
			opts.spiral = true
		case "--focus":
			opts.focus = true
		case "--name":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf(spawnUsage)
			}
			opts.name = args[i+1]
			i++
		case "--host":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf(spawnUsage)
			}
			opts.host = args[i+1]
			i++
		case "--task":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf(spawnUsage)
			}
			opts.task = args[i+1]
			i++
		case "--color":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf(spawnUsage)
			}
			opts.color = args[i+1]
			i++
		default:
			return "", nil, fmt.Errorf(spawnUsage)
		}
	}

	if opts.spiral && (opts.at != "" || opts.root || opts.hasExplicitDir) {
		return "", nil, fmt.Errorf(spawnUsage)
	}

	cmdArgs := make([]string, 0, 10)
	if opts.spiral {
		if opts.host != "" {
			cmdArgs = append(cmdArgs, "--host", opts.host)
		}
		if opts.name != "" {
			cmdArgs = append(cmdArgs, "--name", opts.name)
		}
		if opts.task != "" {
			cmdArgs = append(cmdArgs, "--task", opts.task)
		}
		if opts.color != "" {
			cmdArgs = append(cmdArgs, "--color", opts.color)
		}
		if opts.focus {
			return "add-pane-focus", cmdArgs, nil
		}
		return "add-pane", cmdArgs, nil
	}

	if opts.at != "" || opts.root || opts.hasExplicitDir {
		if opts.at != "" {
			cmdArgs = append(cmdArgs, opts.at)
		}
		if opts.root {
			cmdArgs = append(cmdArgs, "root")
		}
		if opts.dir == mux.SplitVertical {
			cmdArgs = append(cmdArgs, "v")
		}
		if opts.host != "" {
			cmdArgs = append(cmdArgs, "--host", opts.host)
		}
		if opts.name != "" {
			cmdArgs = append(cmdArgs, "--name", opts.name)
		}
		if opts.task != "" {
			cmdArgs = append(cmdArgs, "--task", opts.task)
		}
		if opts.color != "" {
			cmdArgs = append(cmdArgs, "--color", opts.color)
		}
		if opts.focus {
			return "split-focus", cmdArgs, nil
		}
		return "split", cmdArgs, nil
	}

	if opts.name != "" {
		cmdArgs = append(cmdArgs, "--name", opts.name)
	}
	if opts.host != "" {
		cmdArgs = append(cmdArgs, "--host", opts.host)
	}
	if opts.task != "" {
		cmdArgs = append(cmdArgs, "--task", opts.task)
	}
	if opts.color != "" {
		cmdArgs = append(cmdArgs, "--color", opts.color)
	}
	if opts.focus {
		return "spawn-focus", cmdArgs, nil
	}
	return "spawn", cmdArgs, nil
}

func parseEqualizeArgs(args []string) ([]string, error) {
	mode := ""
	for _, arg := range args {
		switch arg {
		case "--vertical", "--all":
			if mode != "" && mode != arg {
				return nil, fmt.Errorf("conflicting equalize modes")
			}
			mode = arg
		default:
			return nil, fmt.Errorf("unknown equalize arg %q", arg)
		}
	}
	if mode == "" {
		return nil, nil
	}
	return []string{mode}, nil
}

func printUsage() {
	fmt.Println(`amux — Agent-Centric Terminal Multiplexer

Usage:
  amux [-s session]                    Start or attach to amux session
  amux [-s session] new [name]         Start or attach to a named session
  amux [-s session] list [--no-cwd]    List panes with metadata
  amux [-s session] status             Show pane/window summary
  amux [-s session] list-clients       List attached clients + client-local UI state
  amux [-s session] log clients        Show recent client attach/detach history
  amux [-s session] log panes          Show pane create/exit history with exit cwd/branch context
  amux [-s session] capture            Capture full composited screen
  amux [-s session] capture --history --format json
                                       Capture full-session JSON with per-pane scrollback prepended to content
  amux [-s session] capture <pane>     Capture a single pane's output
  amux [-s session] capture --history <pane>
                                       Capture a pane's retained history + visible screen
  amux [-s session] capture --ansi     Capture with ANSI escape codes
  amux [-s session] capture --colors   Capture border color map
  amux [-s session] send-keys <pane> [--via pty|client] [--wait ready|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>...
                                       Send keystrokes to a pane
  amux [-s session] broadcast (--panes <pane,pane,...> | --window <index|name> | --match <glob>) [--hex] <keys>...
                                       Send the same keystrokes to multiple panes
  amux [-s session] spawn [--at <pane>] [--vertical|--horizontal] [--root] [--spiral] [--focus] [--name NAME] [--host HOST] [--task TASK] [--color COLOR]
                                       Create a new pane using split, spiral, or default spawn placement
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
  amux [-s session] resize-pane <pane> <dir> [n]
                                       Resize pane (dir: left/right/up/down)
  amux [-s session] equalize [--vertical|--all]
                                       Rebalance root columns, column rows, or both
  amux [-s session] kill <pane>        Kill a pane
  amux [-s session] undo              Undo last pane close
  amux [-s session] focus <pane>       Focus a pane by name or ID
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
  amux [-s session] rename-window <n>  Rename the active window
  amux [-s session] resize-window <c> <r>
                                       Resize window to cols x rows
  amux [-s session] events [--filter type1,type2] [--pane <ref>] [--host <name>] [--client <id>] [--no-reconnect]
                                       Stream events as NDJSON (layout, output, idle, busy, exited, client-connect, client-disconnect, display-panes-*, choose-*, copy-mode-*, input-*, reconnect)
  amux [-s session] hosts              List configured remote hosts + status
  amux [-s session] disconnect <host>  Drop SSH connection to a host
  amux [-s session] reconnect <host>   Reconnect to a remote host
  amux [-s session] unsplice <host>    Revert SSH takeover for a host
  amux [-s session] reload-server      Hot-reload the server (preserves panes)
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
                                       Block until pane has child processes
  amux [-s session] wait ready <pane> [--timeout 10s]
                                       Block until pane VT output settles and no foreground child processes remain
  amux [-s session] wait exited <pane> [--timeout 5s]
                                       Block until pane has no foreground child processes
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
  Ctrl-a s                           Open window/pane chooser
  Ctrl-a w                           Open window chooser
  Ctrl-a q                           Show pane labels for quick jump
  Ctrl-a 1-9                         Select window by number
  Ctrl-a r                           Hot reload (re-exec binary)
  Ctrl-a d                           Detach from session
  Ctrl-a a                           Add pane in clockwise spiral order
  Ctrl-a Ctrl-a                      Send literal Ctrl-a

See https://github.com/weill-labs/amux for config format.`)
}

func looksLikePaneRefArg(arg string) bool {
	if strings.HasPrefix(arg, "pane-") {
		return true
	}
	for _, r := range arg {
		if r < '0' || r > '9' {
			return false
		}
	}
	return arg != ""
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

func restoreServerFromReloadCheckpoint(sessionName, cpPath string, scrollbackLines int) (*server.Server, error) {
	cp, err := checkpoint.Read(cpPath)
	if err == nil {
		return server.NewServerFromCheckpointWithScrollback(cp, scrollbackLines)
	}

	var versionErr checkpoint.UnsupportedServerCheckpointVersionError
	if !errors.As(err, &versionErr) {
		return nil, err
	}
	// checkpoint.Read only reports UnsupportedServerCheckpointVersionError after
	// decoding the checkpoint, so cp is non-nil here and still carries the
	// inherited listener FD needed for crash fallback.

	restoreSessionName := sessionName
	if cp.SessionName != "" {
		restoreSessionName = cp.SessionName
	}

	crashPaths := checkpoint.FindCrashCheckpoints(restoreSessionName)
	if len(crashPaths) == 0 {
		return nil, fmt.Errorf("%w; no crash checkpoint fallback found", err)
	}

	crashPath := crashPaths[0]
	crashCP, crashErr := checkpoint.ReadCrash(crashPath)
	if crashErr != nil {
		return nil, fmt.Errorf("%w; crash fallback %s: %v", err, crashPath, crashErr)
	}
	if cp.ListenerFd <= 0 {
		return nil, fmt.Errorf("%w; invalid listener fd %d in reload checkpoint", err, cp.ListenerFd)
	}

	fmt.Fprintf(os.Stderr, "amux server: reload checkpoint incompatible, falling back to crash checkpoint %s\n", crashPath)
	return server.NewServerFromCrashCheckpointWithListenerFd(restoreSessionName, cp.ListenerFd, crashCP, crashPath, scrollbackLines)
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
		s, err = restoreServerFromReloadCheckpoint(sessionName, cpPath, scrollbackLines)
		if err != nil {
			fmt.Fprintf(os.Stderr, "amux server: reading checkpoint: %v\n", err)
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

	metaRefreshEnabled := os.Getenv("AMUX_DISABLE_META_REFRESH") != "1"
	s.SetPaneMetaAutoRefresh(metaRefreshEnabled)

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
	if execErr == nil && !s.Env.NoWatch {
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
		fmt.Fprintf(os.Stderr, "amux events: %v\n", err)
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

func dialServer(sessionName string) (net.Conn, error) {
	conn, err := net.Dial("unix", server.SocketPath(sessionName))
	if err != nil {
		return nil, fmt.Errorf("connecting to server: %w", err)
	}
	return conn, nil
}

func connectStreamingCommand(sessionName, cmdName string, args []string) (net.Conn, error) {
	conn, err := dialServer(sessionName)
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
	conn, err := dialServer(sessionName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux %s: %v\n", cmdName, err)
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
