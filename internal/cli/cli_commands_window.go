package cli

import "fmt"

func windowCLICommands() map[string]commandHandler {
	return map[string]commandHandler{
		"new-window": func(inv invocation, args []string) int {
			return inv.runSessionCommand("new-window", args)
		},
		"list-windows": func(inv invocation, args []string) int {
			return inv.runSessionCommand("list-windows", nil)
		},
		"select-window": func(inv invocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.Stderr, "usage: amux select-window <index|name>")
				return 1
			}
			return inv.runSessionCommand("select-window", []string{args[0]})
		},
		"next-window": func(inv invocation, args []string) int {
			return inv.runSessionCommand("next-window", nil)
		},
		"prev-window": func(inv invocation, args []string) int {
			return inv.runSessionCommand("prev-window", nil)
		},
		"last-window": func(inv invocation, args []string) int {
			return inv.runSessionCommand("last-window", nil)
		},
		"rename-window": func(inv invocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.Stderr, "usage: amux rename-window <name>")
				return 1
			}
			return inv.runSessionCommand("rename-window", []string{args[0]})
		},
		"resize-window": func(inv invocation, args []string) int {
			if len(args) < 2 {
				fmt.Fprintln(inv.runtime.Stderr, "usage: amux resize-window <cols> <rows>")
				return 1
			}
			return inv.runSessionCommand("resize-window", args)
		},
	}
}
