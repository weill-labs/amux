package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/weill-labs/amux/internal/checkpoint"
)

// BuildCommit can be set via -ldflags "-X main.BuildCommit=abc1234".
// Falls back to VCS info from runtime/debug at startup.
var BuildCommit string

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

func main() {
	os.Exit(runMain(os.Args[1:], defaultCLIRuntime()))
}
