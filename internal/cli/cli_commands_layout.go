package cli

import (
	"fmt"

	"github.com/weill-labs/amux/internal/server"
)

func layoutCLICommands() map[string]commandHandler {
	return map[string]commandHandler{
		"list": func(inv invocation, args []string) int {
			return inv.runSessionCommand("list", args)
		},
		"status": func(inv invocation, args []string) int {
			return inv.runSessionCommand("status", nil)
		},
		"list-clients": func(inv invocation, args []string) int {
			return inv.runSessionCommand("list-clients", nil)
		},
		"log": func(inv invocation, args []string) int {
			cmdName, cmdArgs, err := ParseLogArgs(args)
			if err != nil {
				fmt.Fprintln(inv.runtime.Stderr, logUsage)
				return 1
			}
			return inv.runSessionCommand(cmdName, cmdArgs)
		},
		"capture": func(inv invocation, args []string) int {
			return inv.runSessionCommand("capture", args)
		},
		"copy-mode": func(inv invocation, args []string) int {
			return inv.runSessionCommand("copy-mode", args)
		},
		"cursor": func(inv invocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.Stderr, "usage: amux cursor <layout|clipboard|ui> [--client <id>]")
				return 1
			}
			return inv.runSessionCommand("cursor", args)
		},
		"zoom": func(inv invocation, args []string) int {
			return inv.runSessionCommand("zoom", args)
		},
		"undo": func(inv invocation, args []string) int {
			return inv.runSessionCommand("undo", nil)
		},
		"swap": func(inv invocation, args []string) int {
			cmdName, cmdArgs, err := ParseSwapArgs(args)
			if err != nil {
				fmt.Fprintln(inv.runtime.Stderr, swapUsage)
				return 1
			}
			return inv.runSessionCommand(cmdName, cmdArgs)
		},
		"move": func(inv invocation, args []string) int {
			cmdName, cmdArgs, err := ParseMoveArgs(args)
			if err != nil {
				fmt.Fprintln(inv.runtime.Stderr, moveUsage)
				return 1
			}
			return inv.runSessionCommand(cmdName, cmdArgs)
		},
		"rotate": func(inv invocation, args []string) int {
			return inv.runSessionCommand("rotate", args)
		},
		"resize-pane": func(inv invocation, args []string) int {
			if len(args) < 2 {
				fmt.Fprintln(inv.runtime.Stderr, "usage: amux resize-pane <pane> <direction> [delta]")
				return 1
			}
			return inv.runSessionCommand("resize-pane", args)
		},
		"equalize": func(inv invocation, args []string) int {
			equalizeArgs, err := ParseEqualizeArgs(args)
			if err != nil {
				fmt.Fprintf(inv.runtime.Stderr, "amux equalize: %v\n", err)
				fmt.Fprintln(inv.runtime.Stderr, "usage: amux equalize [--vertical|--all]")
				return 1
			}
			return inv.runSessionCommand("equalize", equalizeArgs)
		},
		"reset": func(inv invocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.Stderr, "usage: amux reset <pane>")
				return 1
			}
			return inv.runSessionCommand("reset", []string{args[0]})
		},
		"respawn": func(inv invocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.Stderr, respawnUsage)
				return 1
			}
			return inv.runSessionCommand("respawn", []string{args[0]})
		},
		"focus": func(inv invocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.Stderr, "usage: amux focus <pane>")
				return 1
			}
			return inv.runSessionCommand("focus", []string{args[0]})
		},
		"kill": func(inv invocation, args []string) int {
			if err := server.ValidateKillCommandArgs(args); err != nil {
				fmt.Fprintln(inv.runtime.Stderr, server.FormatKillCommandError(err, "amux"))
				return 1
			}
			return inv.runSessionCommand("kill", args)
		},
		"send-keys": func(inv invocation, args []string) int {
			if handled, exitCode := MaybePrintKeyCommandUsage(inv.runtime.Stdout, inv.runtime.Stderr, args, sendKeysUsage, 2); handled {
				return exitCode
			}
			return inv.runSessionCommand("send-keys", args)
		},
		"mouse": func(inv invocation, args []string) int {
			if hasHelpFlag(args) {
				fmt.Fprintln(inv.runtime.Stdout, mouseUsage)
				return 0
			}
			if len(args) == 0 {
				fmt.Fprintln(inv.runtime.Stderr, mouseUsage)
				return 1
			}
			return inv.runSessionCommand("mouse", args)
		},
		"broadcast": func(inv invocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.Stderr, "usage: amux broadcast (--panes <pane,pane,...> | --window <index|name> | --match <glob>) [--hex] <keys>...")
				return 1
			}
			return inv.runSessionCommand("broadcast", args)
		},
		"spawn": func(inv invocation, args []string) int {
			cmdName, cmdArgs, err := ParseSpawnCommandArgs(args)
			if err != nil {
				fmt.Fprintln(inv.runtime.Stderr, spawnUsage)
				return 1
			}
			return inv.runSessionCommand(cmdName, cmdArgs)
		},
		"lead": func(inv invocation, args []string) int {
			cmdName, cmdArgs, err := ParseLeadArgs(args)
			if err != nil {
				fmt.Fprintln(inv.runtime.Stderr, leadUsage)
				return 1
			}
			return inv.runSessionCommand(cmdName, cmdArgs)
		},
		"meta": func(inv invocation, args []string) int {
			if err := ValidateMetaArgs(args); err != nil {
				fmt.Fprintln(inv.runtime.Stderr, err)
				return 1
			}
			return inv.runSessionCommand("meta", args)
		},
		"wait": func(inv invocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.Stderr, "usage: amux wait <idle|busy|exited|ready|content|layout|clipboard|checkpoint|ui> ...")
				return 1
			}
			return inv.runSessionCommand("wait", args)
		},
	}
}
