package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/server"
)

const DefaultSessionName = server.DefaultSessionName

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
	"last-window":   {connectName: "last-window", usage: lastWindowUsage, argMode: sessionCommandNoArgs},
	"rename":        {connectName: "rename", minArgs: 2, usage: renameUsage, argMode: sessionCommandForwardArgs},
	"reconnect":     {connectName: "reconnect", minArgs: 1, usage: reconnectUsage, argMode: sessionCommandFirstArg},
	"reload-server": {connectName: "reload-server", usage: reloadServerUsage, argMode: sessionCommandNoArgs},
	"rename-window": {connectName: "rename-window", minArgs: 1, usage: renameWindowUsage, argMode: sessionCommandFirstArg},
	"reset":         {connectName: "reset", minArgs: 1, usage: resetUsage, argMode: sessionCommandFirstArg},
	"respawn":       {connectName: "respawn", minArgs: 1, usage: respawnUsage, argMode: sessionCommandFirstArg},
	"resize-window": {connectName: "resize-window", minArgs: 2, usage: resizeWindowUsage, argMode: sessionCommandForwardArgs},
	"rotate":        {connectName: "rotate", usage: rotateUsage, argMode: sessionCommandForwardArgs},
	"select-window": {connectName: "select-window", minArgs: 1, usage: selectWindowUsage, argMode: sessionCommandFirstArg},
	"status":        {connectName: "status", usage: statusUsage, argMode: sessionCommandNoArgs},
	"undo":          {connectName: "undo", usage: undoUsage, argMode: sessionCommandNoArgs},
	"unsplice":      {connectName: "unsplice", minArgs: 1, usage: unspliceUsage, argMode: sessionCommandFirstArg},
	"wait":          {connectName: "wait", minArgs: 1, usage: waitUsage, argMode: sessionCommandForwardArgs},
	"zoom":          {connectName: "zoom", usage: zoomUsage, argMode: sessionCommandForwardArgs},
}

func ResolveSessionName(explicit string, explicitSet bool) string {
	if explicitSet {
		return explicit
	}
	if envSession := os.Getenv("AMUX_SESSION"); envSession != "" {
		return envSession
	}
	return DefaultSessionName
}

func ResolveInvocationSession(args []string) (string, []string) {
	explicit := DefaultSessionName
	explicitSet := false
	for i := 0; i < len(args); i++ {
		if args[i] == "-s" && i+1 < len(args) {
			explicit = args[i+1]
			explicitSet = true
			return ResolveSessionName(explicit, explicitSet), append(args[:i], args[i+2:]...)
		}
	}
	return ResolveSessionName(explicit, explicitSet), args
}

func ResolveCanonicalSessionCommand(args []string) (cmdName string, cmdArgs []string, handled bool, err error) {
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

func ParseLogArgs(args []string) (string, []string, error) {
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

func ParseLeadArgs(args []string) (string, []string, error) {
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

func ValidateMetaArgs(args []string) error {
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

func ParseSwapArgs(args []string) (string, []string, error) {
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

func ParseMoveArgs(args []string) (string, []string, error) {
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
	window         string
	dir            mux.SplitDir
	hasExplicitDir bool
	auto           bool
	root           bool
	focus          bool
	name           string
	host           string
	task           string
	color          string
}

func ParseSpawnCommandArgs(args []string) (string, []string, error) {
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
		case "--window":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf(spawnUsage)
			}
			opts.window = args[i+1]
			i++
		case "--vertical":
			if err := setDir(mux.SplitVertical); err != nil {
				return "", nil, err
			}
		case "--horizontal":
			if err := setDir(mux.SplitHorizontal); err != nil {
				return "", nil, err
			}
		case "--auto":
			opts.auto = true
		case "--root":
			opts.root = true
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

	if opts.window != "" && opts.at != "" {
		return "", nil, fmt.Errorf(spawnUsage)
	}
	if opts.auto && (opts.at != "" || opts.root || opts.hasExplicitDir) {
		return "", nil, fmt.Errorf(spawnUsage)
	}

	cmdArgs := make([]string, 0, 10)
	if opts.auto {
		if opts.window != "" {
			cmdArgs = append(cmdArgs, "--window", opts.window)
		}
		if opts.focus {
			cmdArgs = append(cmdArgs, "--focus")
		}
		cmdArgs = append(cmdArgs, "--auto")
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
		return "spawn", cmdArgs, nil
	}

	if opts.at != "" {
		cmdArgs = append(cmdArgs, "--at", opts.at)
	}
	if opts.window != "" {
		cmdArgs = append(cmdArgs, "--window", opts.window)
	}
	if opts.root {
		cmdArgs = append(cmdArgs, "--root")
	}
	if opts.hasExplicitDir {
		if opts.dir == mux.SplitVertical {
			cmdArgs = append(cmdArgs, "--vertical")
		} else {
			cmdArgs = append(cmdArgs, "--horizontal")
		}
	}

	if opts.focus {
		cmdArgs = append(cmdArgs, "--focus")
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
	return "spawn", cmdArgs, nil
}

func ParseEqualizeArgs(args []string) ([]string, error) {
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
