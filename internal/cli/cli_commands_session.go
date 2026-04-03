package cli

import "fmt"

func sessionCLICommands() map[string]commandHandler {
	return map[string]commandHandler{
		"version": func(inv invocation, args []string) int {
			if err := inv.runtime.WriteVersionOutput(inv.runtime.Stdout, args); err != nil {
				fmt.Fprintln(inv.runtime.Stderr, err)
				return 1
			}
			return 0
		},
		"install-terminfo": func(inv invocation, args []string) int {
			if err := inv.runtime.InstallTerminfo(); err != nil {
				fmt.Fprintf(inv.runtime.Stderr, "amux install-terminfo: %v\n", err)
				return 1
			}
			return 0
		},
		"_server": func(inv invocation, args []string) int {
			name := inv.sessionName
			if len(args) > 0 {
				name = args[0]
			}
			inv.runtime.RunServer(name, false)
			return 0
		},
		"new": func(inv invocation, args []string) int {
			name := inv.sessionName
			if len(args) > 0 {
				name = args[0]
			}
			inv.runtime.CheckNesting(name)
			if err := inv.runtime.AttachSession(name); err != nil {
				fmt.Fprintf(inv.runtime.Stderr, "amux: %v\n", err)
				return 1
			}
			return 0
		},
		"events": func(inv invocation, args []string) int {
			inv.runtime.RunEventsCommand(inv.sessionName, args)
			return 0
		},
	}
}
