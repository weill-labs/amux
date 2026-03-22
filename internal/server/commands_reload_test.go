package server

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCmdReloadServerWithRequestedExecPath(t *testing.T) {
	t.Parallel()

	execPath := filepath.Join(t.TempDir(), "amux")
	if err := os.WriteFile(execPath, []byte("placeholder"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", execPath, err)
	}

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	cc := NewClientConn(serverConn)
	t.Cleanup(func() { cc.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		cmdReloadServer(&CommandContext{
			CC:   cc,
			Srv:  &Server{},
			Args: []string{ReloadServerExecPathFlag, execPath},
		})
	}()

	msg := readMsgWithTimeout(t, clientConn)
	if got := msg.CmdOutput; got != "Server reloading...\n" {
		t.Fatalf("first reply = %q, want reload notice", got)
	}

	msg = readMsgWithTimeout(t, clientConn)
	if got := msg.CmdErr; got != "no session to reload" {
		t.Fatalf("second reply = %q, want no session error", got)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cmdReloadServer did not return")
	}
}

func TestCmdReloadServerWithoutRequestedExecPathFallsBack(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	cc := NewClientConn(serverConn)
	t.Cleanup(func() { cc.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		cmdReloadServer(&CommandContext{
			CC:  cc,
			Srv: &Server{},
		})
	}()

	msg := readMsgWithTimeout(t, clientConn)
	if got := msg.CmdOutput; got != "Server reloading...\n" {
		t.Fatalf("first reply = %q, want reload notice", got)
	}

	msg = readMsgWithTimeout(t, clientConn)
	if got := msg.CmdErr; got != "no session to reload" {
		t.Fatalf("second reply = %q, want no session error", got)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cmdReloadServer did not return")
	}
}

func TestCmdReloadServerRejectsMissingExecPathValue(t *testing.T) {
	t.Parallel()

	sess := newSession("reload-missing-exec-path")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	msg := runOneShotCommand(t, sess, []string{ReloadServerExecPathFlag}, func(ctx *CommandContext) {
		ctx.Srv = &Server{}
		cmdReloadServer(ctx)
	})
	if got := msg.CmdErr; got != "reload: missing value for --exec-path" {
		t.Fatalf("cmdReloadServer error = %q", got)
	}
}

func TestCmdReloadServerRejectsUnreadableRequestedExecPath(t *testing.T) {
	t.Parallel()

	sess := newSession("reload-bad-exec-path")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	missingPath := filepath.Join(t.TempDir(), "missing-amux")
	msg := runOneShotCommand(t, sess, []string{ReloadServerExecPathFlag, missingPath}, func(ctx *CommandContext) {
		ctx.Srv = &Server{}
		cmdReloadServer(ctx)
	})
	if !strings.Contains(msg.CmdErr, "reload:") || !strings.Contains(msg.CmdErr, missingPath) {
		t.Fatalf("cmdReloadServer error = %q, want missing path context", msg.CmdErr)
	}
}

func TestCmdReloadServerReportsFallbackResolverError(t *testing.T) {
	origResolve := resolveServerReloadExecPath
	resolveServerReloadExecPath = func() (string, error) {
		return "", errors.New("boom")
	}
	defer func() { resolveServerReloadExecPath = origResolve }()

	sess := newSession("reload-resolve-fail")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	msg := runOneShotCommand(t, sess, nil, func(ctx *CommandContext) {
		ctx.Srv = &Server{}
		cmdReloadServer(ctx)
	})
	if got := msg.CmdErr; got != "reload: boom" {
		t.Fatalf("cmdReloadServer error = %q, want %q", got, "reload: boom")
	}
}
