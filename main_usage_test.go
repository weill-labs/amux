package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestMainCLIUsageHelper(t *testing.T) {
	if os.Getenv("AMUX_MAIN_HELPER") != "1" {
		return
	}

	args := os.Args[1:]
	for i, arg := range args {
		if arg == "--" {
			os.Args = append([]string{"amux"}, args[i+1:]...)
			main()
			return
		}
	}
	t.Fatal("missing -- separator")
}

func runMainUsage(t *testing.T, args ...string) (output string, exitCode int) {
	t.Helper()

	cmdArgs := append([]string{"-test.run=TestMainCLIUsageHelper", "--"}, args...)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Env = append(os.Environ(), "AMUX_MAIN_HELPER=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("helper error = %v\n%s", err, out)
	}
	return string(out), exitErr.ExitCode()
}

func TestMainSendKeysUsageIncludesWaitReadyFlags(t *testing.T) {
	t.Parallel()

	out, code := runMainUsage(t, "send-keys", "pane-1")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux send-keys <pane> [--wait-ready] [--continue-known-dialogs] [--hex] <keys>...") {
		t.Fatalf("usage output missing wait-ready flags:\n%s", out)
	}
}

func TestMainWaitReadyUsage(t *testing.T) {
	t.Parallel()

	out, code := runMainUsage(t, "wait-ready")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux wait-ready <pane> [--timeout <duration>] [--continue-known-dialogs]") {
		t.Fatalf("wait-ready usage output = %q", out)
	}
}

func TestMainWaitVTIdleUsage(t *testing.T) {
	t.Parallel()

	out, code := runMainUsage(t, "wait-vt-idle")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux wait-vt-idle <pane> [--settle <duration>] [--timeout <duration>]") {
		t.Fatalf("wait-vt-idle usage output = %q", out)
	}
}

func TestMainKillAllowsImplicitActivePane(t *testing.T) {
	t.Parallel()

	out, code := runMainUsage(t, "kill")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "usage: amux kill") {
		t.Fatalf("kill should accept an omitted pane, got usage output:\n%s", out)
	}
	if !strings.Contains(out, "amux kill: server not running") {
		t.Fatalf("kill without a running server should attempt the command, got:\n%s", out)
	}
}

func TestMainKillUsageRejectsTimeoutWithoutCleanup(t *testing.T) {
	t.Parallel()

	out, code := runMainUsage(t, "kill", "--timeout", "1s")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux kill [--cleanup] [--timeout <duration>] [pane]") {
		t.Fatalf("kill usage output = %q", out)
	}
}

func TestMainResetUsage(t *testing.T) {
	t.Parallel()

	out, code := runMainUsage(t, "reset")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux reset <pane>") {
		t.Fatalf("reset usage output = %q", out)
	}
}
