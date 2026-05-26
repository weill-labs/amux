package test

import (
	"crypto/rand"
	"errors"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"
)

func mustRandRead(tb testing.TB, data []byte) {
	tb.Helper()
	if _, err := rand.Read(data); err != nil {
		tb.Fatalf("rand.Read() error = %v", err)
	}
}

func mustRun(tb testing.TB, cmd *exec.Cmd) {
	tb.Helper()
	if err := cmd.Run(); err != nil {
		tb.Fatalf("Run() error = %v", err)
	}
}

func mustWriteFile(tb testing.TB, path string, data []byte, perm os.FileMode) {
	tb.Helper()
	if err := os.WriteFile(path, data, perm); err != nil {
		tb.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func mustMkdirAll(tb testing.TB, path string, perm os.FileMode) {
	tb.Helper()
	if err := os.MkdirAll(path, perm); err != nil {
		tb.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
}

func mustSetReadDeadline(tb testing.TB, conn interface{ SetReadDeadline(time.Time) error }, deadline time.Time) {
	tb.Helper()
	if err := conn.SetReadDeadline(deadline); err != nil {
		tb.Fatalf("SetReadDeadline() error = %v", err)
	}
}

func mustWrite(tb testing.TB, writer interface{ Write([]byte) (int, error) }, data []byte) {
	tb.Helper()
	if _, err := writer.Write(data); err != nil {
		tb.Fatalf("Write() error = %v", err)
	}
}

func ignoreProcessKill(proc *os.Process) {
	if proc == nil {
		return
	}
	err := proc.Kill()
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		_ = err //nolint:errcheck // teardown races are acceptable when the process is already gone
	}
}

func mustExitError(tb testing.TB, err error, out []byte) *exec.ExitError {
	tb.Helper()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		tb.Fatalf("expected *exec.ExitError, got %v\n%s", err, out)
	}
	return exitErr
}

func exitErrorCode(err error) (int, bool) {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 0, false
	}
	return exitErr.ExitCode(), true
}

func isTimeoutNetError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
