package cli

import "fmt"

func mcpCLICommands() map[string]commandHandler {
	return map[string]commandHandler{
		"mcp-server": func(inv invocation, args []string) int {
			if len(args) != 0 {
				fmt.Fprintln(inv.runtime.Stderr, mcpServerUsage)
				return 1
			}
			inv.runtime.RunMCPServer(inv.sessionName)
			return 0
		},
	}
}
