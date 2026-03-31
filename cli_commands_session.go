package main

import "fmt"

func sessionCLICommands() map[string]cliCommandHandler {
	return map[string]cliCommandHandler{
		"version": func(inv cliInvocation, args []string) int {
			if err := inv.runtime.writeVersionOutput(inv.runtime.stdout, args); err != nil {
				fmt.Fprintln(inv.runtime.stderr, err)
				return 1
			}
			return 0
		},
		"install-terminfo": func(inv cliInvocation, args []string) int {
			if err := inv.runtime.installTerminfo(); err != nil {
				fmt.Fprintf(inv.runtime.stderr, "amux install-terminfo: %v\n", err)
				return 1
			}
			return 0
		},
		"_server": func(inv cliInvocation, args []string) int {
			name := inv.sessionName
			if len(args) > 0 {
				name = args[0]
			}
			inv.runtime.runServer(name, false)
			return 0
		},
		"new": func(inv cliInvocation, args []string) int {
			name := inv.sessionName
			if len(args) > 0 {
				name = args[0]
			}
			inv.runtime.checkNesting(name)
			if err := inv.runtime.attachSession(name); err != nil {
				fmt.Fprintf(inv.runtime.stderr, "amux: %v\n", err)
				return 1
			}
			return 0
		},
		"events": func(inv cliInvocation, args []string) int {
			inv.runtime.runEventsCommand(inv.sessionName, args)
			return 0
		},
	}
}
