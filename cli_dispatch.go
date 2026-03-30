package main

import (
	"fmt"
	"io"
	"os"

	"github.com/weill-labs/amux/internal/client"
	"github.com/weill-labs/amux/internal/terminfo"
	"golang.org/x/term"
)

type cliRuntime struct {
	stdout             io.Writer
	stderr             io.Writer
	attachSession      func(string) error
	writeVersionOutput func(io.Writer, []string) error
	installTerminfo    func() error
	runServer          func(string, bool)
	runServerCommand   func(string, string, []string)
	runEventsCommand   func(string, []string)
	checkNesting       func(string)
	shouldTakeover     func() bool
	tryTakeover        func(string) bool
	printUsage         func()
}

type cliInvocation struct {
	runtime     cliRuntime
	sessionName string
}

type cliCommandHandler func(cliInvocation, []string) int

var cliCommands = buildCLICommands()

func defaultCLIRuntime() cliRuntime {
	return cliRuntime{
		stdout: os.Stdout,
		stderr: os.Stderr,
		attachSession: func(sessionName string) error {
			return client.RunSession(sessionName, term.GetSize)
		},
		writeVersionOutput: writeVersionOutput,
		installTerminfo:    terminfo.Install,
		runServer:          runServer,
		runServerCommand:   runServerCommand,
		runEventsCommand:   runEventsCommand,
		checkNesting:       checkNesting,
		shouldTakeover:     shouldAttemptTakeover,
		tryTakeover:        tryTakeover,
		printUsage:         printUsage,
	}
}

func buildCLICommands() map[string]cliCommandHandler {
	commands := make(map[string]cliCommandHandler)
	addCLICommands(commands, sessionCLICommands())
	addCLICommands(commands, layoutCLICommands())
	addCLICommands(commands, windowCLICommands())
	addCLICommands(commands, remoteCLICommands())
	return commands
}

func addCLICommands(dst, src map[string]cliCommandHandler) {
	for name, handler := range src {
		if _, exists := dst[name]; exists {
			panic("duplicate CLI command: " + name)
		}
		dst[name] = handler
	}
}

func runMain(args []string, runtime cliRuntime) int {
	return runCLI(runtime, args)
}

func runCLI(runtime cliRuntime, rawArgs []string) int {
	sessionName, args := resolveInvocationSession(rawArgs)
	invocation := cliInvocation{runtime: runtime, sessionName: sessionName}

	if os.Getenv("AMUX_CHECKPOINT") != "" {
		runtime.runServer(sessionName, false)
		return 0
	}
	if len(args) == 0 {
		return invocation.runDefaultSession()
	}
	if maybePrintCommandHelp(runtime.stdout, args) {
		return 0
	}
	if args[0] == "help" || isHelpFlag(args[0]) {
		runtime.printUsage()
		return 0
	}

	handler, ok := cliCommands[args[0]]
	if !ok {
		fmt.Fprintf(runtime.stderr, "amux: unknown command %q\n", args[0])
		runtime.printUsage()
		return 1
	}
	return handler(invocation, args[1:])
}

func (inv cliInvocation) runDefaultSession() int {
	if inv.runtime.shouldTakeover() && inv.runtime.tryTakeover(inv.sessionName) {
		return 0
	}
	inv.runtime.checkNesting(inv.sessionName)
	if err := inv.runtime.attachSession(inv.sessionName); err != nil {
		fmt.Fprintf(inv.runtime.stderr, "amux: %v\n", err)
		return 1
	}
	return 0
}

func (inv cliInvocation) runSessionCommand(cmdName string, args []string) int {
	inv.runtime.runServerCommand(inv.sessionName, cmdName, args)
	return 0
}
