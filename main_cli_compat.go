package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/weill-labs/amux/internal/checkpoint"
	clicmd "github.com/weill-labs/amux/internal/cli"
	"github.com/weill-labs/amux/internal/client"
	"github.com/weill-labs/amux/internal/terminfo"
	"golang.org/x/term"
)

const (
	defaultSessionName = clicmd.DefaultSessionName

	sendKeysUsage = "usage: amux send-keys <pane> [--via pty|client] [--client <id>] [--wait ready|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>..."
	mouseUsage    = "usage: amux mouse [--client <id>] [--timeout <duration>] (press <x> <y> | motion <x> <y> | release <x> <y> | click <x> <y> | click <pane> [--status-line] | drag <pane> --to <pane>)"
	leadUsage     = "usage: amux lead [pane] | amux lead --clear"
	metaUsage     = "usage: amux meta <set|get|rm> ..."
	moveUsage     = "usage: amux move <pane> up|down | amux move <pane> (--before <target>|--after <target>|--to-column <target>)"
	spawnUsage    = "usage: amux spawn [--at <pane>] [--vertical|--horizontal] [--root] [--spiral] [--focus] [--name NAME] [--host HOST] [--task TASK] [--color COLOR]"
	swapUsage     = "usage: amux swap <pane1> <pane2> [--tree] | amux swap forward | amux swap backward"
	waitUsage     = "usage: amux wait <idle|busy|exited|ready|content|layout|clipboard|checkpoint|ui> ..."
)

var commandUsageByName = map[string]string{
	"_inject-proxy":    "",
	"_layout-json":     "",
	"_server":          "",
	"broadcast":        "",
	"capture":          "",
	"copy-mode":        "",
	"cursor":           "",
	"disconnect":       "",
	"equalize":         "",
	"events":           "",
	"focus":            "",
	"hosts":            "",
	"install-terminfo": "",
	"kill":             "",
	"lead":             "",
	"list":             "",
	"list-clients":     "",
	"list-windows":     "",
	"log":              "",
	"meta":             "",
	"mouse":            "",
	"move":             "",
	"new":              "",
	"new-window":       "",
	"next-window":      "",
	"prev-window":      "",
	"last-window":      "",
	"reconnect":        "",
	"reload-server":    "",
	"rename-window":    "",
	"reset":            "",
	"respawn":          "",
	"resize-pane":      "",
	"resize-window":    "",
	"rotate":           "",
	"select-window":    "",
	"send-keys":        "",
	"spawn":            "",
	"status":           "",
	"undo":             "",
	"swap":             "",
	"unsplice":         "",
	"version":          "",
	"wait":             "",
	"zoom":             "",
}

type versionInfo struct {
	Build             string `json:"build"`
	CheckpointVersion int    `json:"checkpoint_version"`
}

func (v versionInfo) String() string {
	return fmt.Sprintf("%s (checkpoint v%d)", v.Build, v.CheckpointVersion)
}

func currentVersionInfo() versionInfo {
	return versionInfo{
		Build:             buildHash(),
		CheckpointVersion: checkpoint.ServerCheckpointVersion,
	}
}

// buildHash returns the build identifier (commit hash or "dev").
func buildHash() string {
	if BuildCommit != "" {
		return BuildCommit
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				return s.Value[:7]
			}
		}
	}
	return "dev"
}

func buildVersion() string {
	return currentVersionInfo().String()
}

func writeVersionOutput(w io.Writer, args []string) error {
	switch len(args) {
	case 0:
		_, err := fmt.Fprintf(w, "amux build: %s\n", buildVersion())
		return err
	case 1:
		switch args[0] {
		case "--hash":
			_, err := fmt.Fprintln(w, buildHash())
			return err
		case "--json":
			return json.NewEncoder(w).Encode(currentVersionInfo())
		}
	}
	return fmt.Errorf("usage: amux version [--hash|--json]")
}

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
		runServerCommand:   clicmd.RunServerCommand,
		runEventsCommand:   clicmd.RunEventsCommand,
		checkNesting:       checkNesting,
		shouldTakeover:     shouldAttemptTakeover,
		tryTakeover:        tryTakeover,
		printUsage:         printUsage,
	}
}

func (r cliRuntime) internal() clicmd.Runtime {
	return clicmd.Runtime{
		Stdout:             r.stdout,
		Stderr:             r.stderr,
		AttachSession:      r.attachSession,
		WriteVersionOutput: r.writeVersionOutput,
		InstallTerminfo:    r.installTerminfo,
		RunServer:          r.runServer,
		RunServerCommand:   r.runServerCommand,
		RunEventsCommand:   r.runEventsCommand,
		CheckNesting:       r.checkNesting,
		ShouldTakeover:     r.shouldTakeover,
		TryTakeover:        r.tryTakeover,
		PrintUsage:         r.printUsage,
	}
}

func runMain(args []string, runtime cliRuntime) int {
	return clicmd.RunWithRuntime(args, runtime.internal())
}

func resolveSessionName(explicit string, explicitSet bool) string {
	return clicmd.ResolveSessionName(explicit, explicitSet)
}

func resolveInvocationSession(args []string) (string, []string) {
	return clicmd.ResolveInvocationSession(args)
}

func resolveCanonicalSessionCommand(args []string) (string, []string, bool, error) {
	return clicmd.ResolveCanonicalSessionCommand(args)
}

func parseLeadArgs(args []string) (string, []string, error) {
	return clicmd.ParseLeadArgs(args)
}

func validateMetaArgs(args []string) error {
	return clicmd.ValidateMetaArgs(args)
}

func parseSwapArgs(args []string) (string, []string, error) {
	return clicmd.ParseSwapArgs(args)
}

func parseMoveArgs(args []string) (string, []string, error) {
	return clicmd.ParseMoveArgs(args)
}

func parseSpawnCommandArgs(args []string) (string, []string, error) {
	return clicmd.ParseSpawnCommandArgs(args)
}

func parseEqualizeArgs(args []string) ([]string, error) {
	return clicmd.ParseEqualizeArgs(args)
}

func maybePrintCommandHelp(stdout io.Writer, args []string) bool {
	return clicmd.MaybePrintCommandHelp(stdout, args)
}

func maybePrintKeyCommandUsage(stdout, stderr io.Writer, args []string, usage string, minArgs int) (bool, int) {
	return clicmd.MaybePrintKeyCommandUsage(stdout, stderr, args, usage, minArgs)
}

func printUsage() {
	clicmd.WriteUsage(os.Stdout)
}

func prependReloadExecPathArg(resolve func() (string, error), args []string) []string {
	return clicmd.PrependReloadExecPathArg(resolve, args)
}
