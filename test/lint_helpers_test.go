package test

import (
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
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

func ignoreCmdWait(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	_ = cmd.Wait() //nolint:errcheck // shutdown-path tests intentionally reap signaled processes
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

func ignoreReject(newChannel ssh.NewChannel, reason ssh.RejectionReason, message string) {
	_ = newChannel.Reject(reason, message) //nolint:errcheck // test SSH server teardown can race channel shutdown
}

func ignoreReply(req *ssh.Request, ok bool) {
	_ = req.Reply(ok, nil) //nolint:errcheck // test SSH request channel may already be closed
}

func ignoreSendRequest(ch ssh.Channel, name string, wantReply bool, payload []byte) {
	_, _ = ch.SendRequest(name, wantReply, payload) //nolint:errcheck // exit-status notification is best-effort in tests
}

func ignoreCopy(dst io.Writer, src io.Reader) {
	_, _ = io.Copy(dst, src) //nolint:errcheck // SSH test tunnel teardown can interrupt pipe copies
}

func ignoreCloseWrite(ch ssh.Channel) {
	_ = ch.CloseWrite() //nolint:errcheck // test SSH channel may already be closing
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
