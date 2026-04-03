package cli

import (
	"fmt"
	"io"
	"os"
)

type Runtime struct {
	Stdout             io.Writer
	Stderr             io.Writer
	AttachSession      func(string) error
	WriteVersionOutput func(io.Writer, []string) error
	InstallTerminfo    func() error
	RunDebugCommand    func(string, []string)
	RunServer          func(string, bool)
	RunServerCommand   func(string, string, []string)
	RunEventsCommand   func(string, []string)
	CheckNesting       func(string)
	ShouldTakeover     func() bool
	TryTakeover        func(string) bool
	PrintUsage         func()
}

type invocation struct {
	runtime     Runtime
	sessionName string
}

type commandHandler func(invocation, []string) int

var commands = buildCLICommands()

func RunWithRuntime(args []string, runtime Runtime) int {
	return runCLI(runtime, args)
}

func buildCLICommands() map[string]commandHandler {
	commands := make(map[string]commandHandler)
	addCLICommands(commands, sessionCLICommands())
	addCLICommands(commands, layoutCLICommands())
	addCLICommands(commands, windowCLICommands())
	addCLICommands(commands, remoteCLICommands())
	return commands
}

func addCLICommands(dst, src map[string]commandHandler) {
	for name, handler := range src {
		if _, exists := dst[name]; exists {
			panic("duplicate CLI command: " + name)
		}
		dst[name] = handler
	}
}

func runCLI(runtime Runtime, rawArgs []string) int {
	sessionName, args := ResolveInvocationSession(rawArgs)
	invocation := invocation{runtime: runtime, sessionName: sessionName}

	if os.Getenv("AMUX_CHECKPOINT") != "" {
		runtime.RunServer(sessionName, false)
		return 0
	}
	if len(args) == 0 {
		return invocation.runDefaultSession()
	}
	if MaybePrintCommandHelp(runtime.Stdout, args) {
		return 0
	}
	if args[0] == "help" || isHelpFlag(args[0]) {
		runtime.PrintUsage()
		return 0
	}

	handler, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(runtime.Stderr, "amux: unknown command %q\n", args[0])
		runtime.PrintUsage()
		return 1
	}
	return handler(invocation, args[1:])
}

func (inv invocation) runDefaultSession() int {
	if inv.runtime.ShouldTakeover() && inv.runtime.TryTakeover(inv.sessionName) {
		return 0
	}
	inv.runtime.CheckNesting(inv.sessionName)
	if err := inv.runtime.AttachSession(inv.sessionName); err != nil {
		fmt.Fprintf(inv.runtime.Stderr, "amux: %v\n", err)
		return 1
	}
	return 0
}

func (inv invocation) runSessionCommand(cmdName string, args []string) int {
	inv.runtime.RunServerCommand(inv.sessionName, cmdName, args)
	return 0
}
