package cli

import "fmt"

const remoteUsage = "usage: amux remote <hosts|connect|disconnect|reconnect|unsplice|reload-server>"

func remoteCLICommands() map[string]commandHandler {
	return map[string]commandHandler{
		"connect": func(inv invocation, args []string) int {
			if len(args) < 1 {
				fmt.Fprintln(inv.runtime.Stderr, connectUsage)
				return 1
			}
			return inv.runSessionCommand("connect", args)
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

func remoteCLICommandGroup() commandHandler {
	subcommands := remoteCLICommands()
	return func(inv invocation, args []string) int {
		switch {
		case len(args) == 0:
			fmt.Fprintln(inv.runtime.Stderr, remoteUsage)
			return 1
		case isHelpFlag(args[0]):
			fmt.Fprintln(inv.runtime.Stdout, remoteUsage)
			return 0
		case len(args) > 1 && isHelpFlag(args[1]):
			if usage, ok := commandUsageByName[args[0]]; ok {
				fmt.Fprintln(inv.runtime.Stdout, usage)
				return 0
			}
		}

		handler, ok := subcommands[args[0]]
		if !ok {
			fmt.Fprintf(inv.runtime.Stderr, "amux: unknown remote command %q\n", args[0])
			fmt.Fprintln(inv.runtime.Stderr, remoteUsage)
			return 1
		}
		return handler(inv, args[1:])
	}
}
