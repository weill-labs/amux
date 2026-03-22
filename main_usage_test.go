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
