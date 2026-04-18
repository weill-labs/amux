package cli

import "fmt"

func remoteCLICommands() map[string]commandHandler {
	return map[string]commandHandler{
		"connect": func(inv invocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.Stderr, "usage: amux connect <host>")
				return 1
			}
			return inv.runSessionCommand("connect", []string{args[0]})
		},
		"hosts": func(inv invocation, args []string) int {
			return inv.runSessionCommand("hosts", nil)
		},
		"disconnect": func(inv invocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.Stderr, "usage: amux disconnect <host>")
				return 1
			}
			return inv.runSessionCommand("disconnect", []string{args[0]})
		},
		"reconnect": func(inv invocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.Stderr, "usage: amux reconnect <host>")
				return 1
			}
			return inv.runSessionCommand("reconnect", []string{args[0]})
		},
		"unsplice": func(inv invocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.Stderr, "usage: amux unsplice <host>")
				return 1
			}
			return inv.runSessionCommand("unsplice", []string{args[0]})
		},
		"reload-server": func(inv invocation, args []string) int {
			return inv.runSessionCommand("reload-server", nil)
		},
		"_layout-json": func(inv invocation, args []string) int {
			return inv.runSessionCommand("_layout-json", nil)
		},
		"_inject-proxy": func(inv invocation, args []string) int {
			return inv.runSessionCommand("_inject-proxy", args)
		},
	}
}
