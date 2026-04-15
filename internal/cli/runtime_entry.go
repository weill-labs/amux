package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/client"
	"github.com/weill-labs/amux/internal/terminfo"
	"golang.org/x/term"
)

type versionInfo struct {
	Build             string `json:"build"`
	CheckpointVersion int    `json:"checkpoint_version"`
}

func (v versionInfo) String() string {
	return fmt.Sprintf("%s (checkpoint v%d)", v.Build, v.CheckpointVersion)
}

func currentVersionInfo(buildCommit string) versionInfo {
	return versionInfo{
		Build:             buildHash(buildCommit),
		CheckpointVersion: checkpoint.ServerCheckpointVersion,
	}
}

func buildHash(buildCommit string) string {
	if buildCommit != "" {
		return buildCommit
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

func buildVersion(buildCommit string) string {
	return currentVersionInfo(buildCommit).String()
}

func writeVersionOutput(buildCommit string, w io.Writer, args []string) error {
	switch len(args) {
	case 0:
		_, err := fmt.Fprintf(w, "amux build: %s\n", buildVersion(buildCommit))
		return err
	case 1:
		switch args[0] {
		case "--hash":
			_, err := fmt.Fprintln(w, buildHash(buildCommit))
			return err
		case "--json":
			return json.NewEncoder(w).Encode(currentVersionInfo(buildCommit))
		}
	}
	return fmt.Errorf("usage: amux version [--hash|--json]")
}

func defaultRuntime(buildCommit string) Runtime {
	return Runtime{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		AttachSession: func(sessionName string) error {
			return client.RunSession(sessionName, term.GetSize)
		},
		RunSSHSession: client.RunSSHSession,
		WriteVersionOutput: func(w io.Writer, args []string) error {
			return writeVersionOutput(buildCommit, w, args)
		},
		InstallTerminfo: terminfo.Install,
		RunDebugCommand: runDebugCommand,
		RunServer: func(sessionName string, managedTakeover bool) {
			RunServer(sessionName, managedTakeover, buildVersion(buildCommit))
		},
		RunServerCommand: RunServerCommand,
		RunEventsCommand: RunEventsCommand,
		CheckNesting:     CheckNesting,
		ShouldTakeover:   ShouldAttemptTakeover,
		TryTakeover: func(sessionName string) bool {
			return TryTakeover(sessionName, buildVersion(buildCommit))
		},
		PrintUsage: PrintUsage,
	}
}

func Run(buildCommit string, args []string) int {
	return RunWithRuntime(args, defaultRuntime(buildCommit))
}
