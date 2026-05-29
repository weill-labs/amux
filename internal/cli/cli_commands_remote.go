package cli

import "fmt"

func remoteCLICommands() map[string]commandHandler {
	return map[string]commandHandler{
		"remote": func(inv invocation, args []string) int {
			if hasHelpFlag(args) {
				fmt.Fprintln(inv.runtime.Stdout, remoteUsage)
				return 0
			}
			if len(args) == 0 {
				fmt.Fprintln(inv.runtime.Stderr, remoteUsage)
				return 1
			}
			return inv.runSessionCommand("remote", args)
		},
	}
}
