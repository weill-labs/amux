package mux

import (
	"errors"
	"syscall"
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
		{name: "exit code 1", err: fakeExitStatusError{code: 1}, want: "exit 1"},
		{name: "exit code 130", err: fakeExitStatusError{code: 130}, want: "exit 130"},
		{name: "signal", err: fakeExitStatusError{code: -1, status: syscall.WaitStatus(syscall.SIGTERM)}, want: "signal: terminated"},
		{name: "plain error", err: errors.New("wait failed"), want: "wait failed"},
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

type fakeExitStatusError struct {
	code   int
	status syscall.WaitStatus
}

func (e fakeExitStatusError) Error() string {
	return "exit status"
}

func (e fakeExitStatusError) ExitCode() int {
	return e.code
}

func (e fakeExitStatusError) Sys() any {
	return e.status
}
