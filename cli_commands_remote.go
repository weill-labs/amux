package main

import "fmt"

func remoteCLICommands() map[string]cliCommandHandler {
	return map[string]cliCommandHandler{
		"hosts": func(inv cliInvocation, args []string) int {
			return inv.runSessionCommand("hosts", nil)
		},
		"disconnect": func(inv cliInvocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.stderr, "usage: amux disconnect <host>")
				return 1
			}
			return inv.runSessionCommand("disconnect", []string{args[0]})
		},
		"reconnect": func(inv cliInvocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.stderr, "usage: amux reconnect <host>")
				return 1
			}
			return inv.runSessionCommand("reconnect", []string{args[0]})
		},
		"unsplice": func(inv cliInvocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.stderr, "usage: amux unsplice <host>")
				return 1
			}
			return inv.runSessionCommand("unsplice", []string{args[0]})
		},
		"reload-server": func(inv cliInvocation, args []string) int {
			return inv.runSessionCommand("reload-server", nil)
		},
		"_layout-json": func(inv cliInvocation, args []string) int {
			return inv.runSessionCommand("_layout-json", nil)
		},
		"_inject-proxy": func(inv cliInvocation, args []string) int {
			return inv.runSessionCommand("_inject-proxy", args)
		},
		"dashboard": func(inv cliInvocation, args []string) int {
			fmt.Fprintln(inv.runtime.stderr, "amux dashboard: not yet migrated to built-in mux")
			return 1
		},
	}
}
