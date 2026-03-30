package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/server"
)

const defaultSessionName = server.DefaultSessionName

const reconnectEventType = "reconnect"

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
