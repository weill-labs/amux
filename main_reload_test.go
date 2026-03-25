package main

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/reload"
	"github.com/weill-labs/amux/internal/server"
)

func TestPrependReloadExecPathArgIncludesResolvedExecutable(t *testing.T) {
	t.Parallel()

	wantPath, err := reload.ResolveExecutable()
	if err != nil {
		t.Fatalf("ResolveExecutable() error = %v", err)
	}

	got := prependReloadExecPathArg(reload.ResolveExecutable, []string{"reload-server"})
	if len(got) != 3 {
		t.Fatalf("len(prependReloadExecPathArg) = %d, want 3", len(got))
	}
	if got[0] != server.ReloadServerExecPathFlag {
		t.Fatalf("flag = %q, want %q", got[0], server.ReloadServerExecPathFlag)
	}
	if got[1] != wantPath {
		t.Fatalf("exec path = %q, want %q", got[1], wantPath)
	}
	if got[2] != "reload-server" {
		t.Fatalf("trailing args = %v, want [reload-server]", got[2:])
	}
}

func TestPrependReloadExecPathArgLeavesArgsUnchangedOnResolverError(t *testing.T) {
	t.Parallel()

	args := []string{"reload-server"}
	got := prependReloadExecPathArg(func() (string, error) {
		return "", errors.New("boom")
	}, args)
	if len(got) != 1 || got[0] != "reload-server" {
		t.Fatalf("prependReloadExecPathArg() = %v, want %v", got, args)
	}
}

func TestMainCheckpointReloadStartsServerWithoutSubcommand(t *testing.T) {
	t.Parallel()

	cmd := newHermeticMainCmd(t)
	cmd.Env = append(cmd.Env, "AMUX_CHECKPOINT=/definitely/missing")

	out, err := cmd.CombinedOutput()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("helper error = %v\n%s", err, out)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitErr.ExitCode(), out)
	}

	output := string(out)
	if !strings.Contains(output, "amux server: reading checkpoint:") {
		t.Fatalf("expected checkpoint reload to route into server startup, got:\n%s", output)
	}
	if strings.Contains(output, "amux: server not running") {
		t.Fatalf("checkpoint reload should not fall back to client attach path:\n%s", output)
	}
}
