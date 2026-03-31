package main

import "fmt"

func windowCLICommands() map[string]cliCommandHandler {
	return map[string]cliCommandHandler{
		"new-window": func(inv cliInvocation, args []string) int {
			return inv.runSessionCommand("new-window", args)
		},
		"list-windows": func(inv cliInvocation, args []string) int {
			return inv.runSessionCommand("list-windows", nil)
		},
		"select-window": func(inv cliInvocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.stderr, "usage: amux select-window <index|name>")
				return 1
			}
			return inv.runSessionCommand("select-window", []string{args[0]})
		},
		"next-window": func(inv cliInvocation, args []string) int {
			return inv.runSessionCommand("next-window", nil)
		},
		"prev-window": func(inv cliInvocation, args []string) int {
			return inv.runSessionCommand("prev-window", nil)
		},
		"rename-window": func(inv cliInvocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.stderr, "usage: amux rename-window <name>")
				return 1
			}
			return inv.runSessionCommand("rename-window", []string{args[0]})
		},
		"resize-window": func(inv cliInvocation, args []string) int {
			if len(args) < 2 {
				fmt.Fprintln(inv.runtime.stderr, "usage: amux resize-window <cols> <rows>")
				return 1
			}
			return inv.runSessionCommand("resize-window", args)
		},
	}
}
