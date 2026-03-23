package mux

import (
	"fmt"
	"os/exec"
	"testing"
)

func TestFormatExitReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil error", err: nil, want: "exit 0"},
		{name: "exit code 1", err: exitError(t, 1), want: "exit 1"},
		{name: "exit code 130", err: exitError(t, 130), want: "exit 130"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatExitReason(tt.err)
			if got != tt.want {
				t.Errorf("formatExitReason(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

// exitError produces a real ExitError by running a subprocess that exits with
// the given code.
func exitError(t *testing.T, code int) *exec.ExitError {
	t.Helper()
	cmd := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code))
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected exit %d, got nil error", code)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T", err)
	}
	return exitErr
}
