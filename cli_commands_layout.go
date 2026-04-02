package main

import (
	"fmt"

	"github.com/weill-labs/amux/internal/server"
)

func layoutCLICommands() map[string]cliCommandHandler {
	return map[string]cliCommandHandler{
		"list": func(inv cliInvocation, args []string) int {
			return inv.runSessionCommand("list", args)
		},
		"status": func(inv cliInvocation, args []string) int {
			return inv.runSessionCommand("status", nil)
		},
		"list-clients": func(inv cliInvocation, args []string) int {
			return inv.runSessionCommand("list-clients", nil)
		},
		"log": func(inv cliInvocation, args []string) int {
			cmdName, cmdArgs, err := parseLogArgs(args)
			if err != nil {
				fmt.Fprintln(inv.runtime.stderr, logUsage)
				return 1
			}
			return inv.runSessionCommand(cmdName, cmdArgs)
		},
		"capture": func(inv cliInvocation, args []string) int {
			return inv.runSessionCommand("capture", args)
		},
		"copy-mode": func(inv cliInvocation, args []string) int {
			return inv.runSessionCommand("copy-mode", args)
		},
		"cursor": func(inv cliInvocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.stderr, "usage: amux cursor <layout|clipboard|ui> [--client <id>]")
				return 1
			}
			return inv.runSessionCommand("cursor", args)
		},
		"zoom": func(inv cliInvocation, args []string) int {
			return inv.runSessionCommand("zoom", args)
		},
		"undo": func(inv cliInvocation, args []string) int {
			return inv.runSessionCommand("undo", nil)
		},
		"swap": func(inv cliInvocation, args []string) int {
			cmdName, cmdArgs, err := parseSwapArgs(args)
			if err != nil {
				fmt.Fprintln(inv.runtime.stderr, swapUsage)
				return 1
			}
			return inv.runSessionCommand(cmdName, cmdArgs)
		},
		"move": func(inv cliInvocation, args []string) int {
			cmdName, cmdArgs, err := parseMoveArgs(args)
			if err != nil {
				fmt.Fprintln(inv.runtime.stderr, moveUsage)
				return 1
			}
			return inv.runSessionCommand(cmdName, cmdArgs)
		},
		"rotate": func(inv cliInvocation, args []string) int {
			return inv.runSessionCommand("rotate", args)
		},
		"resize-pane": func(inv cliInvocation, args []string) int {
			if len(args) < 2 {
				fmt.Fprintln(inv.runtime.stderr, "usage: amux resize-pane <pane> <direction> [delta]")
				return 1
			}
			return inv.runSessionCommand("resize-pane", args)
		},
		"equalize": func(inv cliInvocation, args []string) int {
			equalizeArgs, err := parseEqualizeArgs(args)
			if err != nil {
				fmt.Fprintf(inv.runtime.stderr, "amux equalize: %v\n", err)
				fmt.Fprintln(inv.runtime.stderr, "usage: amux equalize [--vertical|--all]")
				return 1
			}
			return inv.runSessionCommand("equalize", equalizeArgs)
		},
		"reset": func(inv cliInvocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.stderr, "usage: amux reset <pane>")
				return 1
			}
			return inv.runSessionCommand("reset", []string{args[0]})
		},
		"respawn": func(inv cliInvocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.stderr, respawnUsage)
				return 1
			}
			return inv.runSessionCommand("respawn", []string{args[0]})
		},
		"focus": func(inv cliInvocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.stderr, "usage: amux focus <pane>")
				return 1
			}
			return inv.runSessionCommand("focus", []string{args[0]})
		},
		"kill": func(inv cliInvocation, args []string) int {
			if err := server.ValidateKillCommandArgs(args); err != nil {
				fmt.Fprintln(inv.runtime.stderr, server.FormatKillCommandError(err, "amux"))
				return 1
			}
			return inv.runSessionCommand("kill", args)
		},
		"send-keys": func(inv cliInvocation, args []string) int {
			if handled, exitCode := maybePrintKeyCommandUsage(inv.runtime.stdout, inv.runtime.stderr, args, sendKeysUsage, 2); handled {
				return exitCode
			}
			return inv.runSessionCommand("send-keys", args)
		},
		"mouse": func(inv cliInvocation, args []string) int {
			if hasHelpFlag(args) {
				fmt.Fprintln(inv.runtime.stdout, mouseUsage)
				return 0
			}
			if len(args) == 0 {
				fmt.Fprintln(inv.runtime.stderr, mouseUsage)
				return 1
			}
			return inv.runSessionCommand("mouse", args)
		},
		"broadcast": func(inv cliInvocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.stderr, "usage: amux broadcast (--panes <pane,pane,...> | --window <index|name> | --match <glob>) [--hex] <keys>...")
				return 1
			}
			return inv.runSessionCommand("broadcast", args)
		},
		"spawn": func(inv cliInvocation, args []string) int {
			cmdName, cmdArgs, err := parseSpawnCommandArgs(args)
			if err != nil {
				fmt.Fprintln(inv.runtime.stderr, spawnUsage)
				return 1
			}
			return inv.runSessionCommand(cmdName, cmdArgs)
		},
		"lead": func(inv cliInvocation, args []string) int {
			cmdName, cmdArgs, err := parseLeadArgs(args)
			if err != nil {
				fmt.Fprintln(inv.runtime.stderr, leadUsage)
				return 1
			}
			return inv.runSessionCommand(cmdName, cmdArgs)
		},
		"meta": func(inv cliInvocation, args []string) int {
			if err := validateMetaArgs(args); err != nil {
				fmt.Fprintln(inv.runtime.stderr, err)
				return 1
			}
			return inv.runSessionCommand("meta", args)
		},
		"wait": func(inv cliInvocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.stderr, "usage: amux wait <idle|busy|exited|ready|content|layout|clipboard|checkpoint|ui> ...")
				return 1
			}
			return inv.runSessionCommand("wait", args)
		},
	}
}
