package server

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

const (
	sessionLockHelperModeEnv    = "AMUX_SESSION_LOCK_HELPER_MODE"
	sessionLockHelperSessionEnv = "AMUX_SESSION_LOCK_HELPER_SESSION"
)

func TestSessionLockHelperProcess(t *testing.T) {
	mode := os.Getenv(sessionLockHelperModeEnv)
	if mode == "" {
		return
	}

	session := os.Getenv(sessionLockHelperSessionEnv)
	srv, err := newServerWithScrollbackLogger(session, mux.DefaultScrollbackLines, nil)
	if err != nil {
		fmt.Fprint(os.Stderr, err.Error())
		os.Exit(2)
	}

	switch mode {
	case "hold":
		fmt.Fprintln(os.Stdout, "ready")
		select {}
	case "probe":
		srv.Shutdown()
		fmt.Fprintln(os.Stdout, "started")
	default:
		srv.Shutdown()
		fmt.Fprintf(os.Stderr, "unknown helper mode %q", mode)
		os.Exit(2)
	}
}

func TestNewServerWithScrollbackRejectsDuplicateSessionLockWithoutTouchingLockFile(t *testing.T) {
	t.Parallel()

	session := fmt.Sprintf("session-lock-duplicate-%d", time.Now().UnixNano())
	srv, err := newServerWithScrollbackLogger(session, mux.DefaultScrollbackLines, nil)
	if err != nil {
		t.Fatalf("first newServerWithScrollbackLogger() error = %v", err)
	}
	t.Cleanup(srv.Shutdown)

	lockPath := filepath.Join(SocketDir(), session+".lock")
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("expected lock file at %s, err=%v", lockPath, err)
	}

	marker := []byte("keep-this-marker")
	if err := os.WriteFile(lockPath, marker, 0600); err != nil {
		t.Fatalf("WriteFile(%q): %v", lockPath, err)
	}

	_, err = newServerWithScrollbackLogger(session, mux.DefaultScrollbackLines, nil)
	if err == nil {
		t.Fatal("second newServerWithScrollbackLogger() error = nil, want already running")
	}
	wantErr := fmt.Sprintf("server already running for session %q", session)
	if err.Error() != wantErr {
		t.Fatalf("second newServerWithScrollbackLogger() error = %q, want %q", err.Error(), wantErr)
	}

	got, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", lockPath, err)
	}
	if !bytes.Equal(got, marker) {
		t.Fatalf("lock file contents changed = %q, want %q", got, marker)
	}
}

func TestNewServerWithScrollbackRecoversAfterCrashReleasesLock(t *testing.T) {
	t.Parallel()

	session := fmt.Sprintf("session-lock-crash-%d", time.Now().UnixNano())
	cmd, stderr := startSessionLockHelper(t, "hold", session)

	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("killing helper: %v", err)
	}
	err := cmd.Wait()
	if err == nil {
		t.Fatal("helper exited cleanly, want SIGKILL")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("helper wait error = %v, want *exec.ExitError", err)
	}

	srv, err := newServerWithScrollbackLogger(session, mux.DefaultScrollbackLines, nil)
	if err != nil {
		t.Fatalf("newServerWithScrollbackLogger() after SIGKILL error = %v\nhelper stderr:\n%s", err, stderr.String())
	}
	srv.Shutdown()
}

func TestNewServerWithScrollbackConcurrentStartsYieldSingleWinner(t *testing.T) {
	t.Parallel()

	session := fmt.Sprintf("session-lock-race-%d", time.Now().UnixNano())
	const attempts = 8

	type result struct {
		srv *Server
		err error
	}

	start := make(chan struct{})
	results := make(chan result, attempts)
	for range attempts {
		go func() {
			<-start
			srv, err := newServerWithScrollbackLogger(session, mux.DefaultScrollbackLines, nil)
			results <- result{srv: srv, err: err}
		}()
	}
	close(start)

	var winners []*Server
	for range attempts {
		res := <-results
		if res.err == nil {
			winners = append(winners, res.srv)
			continue
		}
		wantErr := fmt.Sprintf("server already running for session %q", session)
		if res.err.Error() != wantErr {
			t.Fatalf("concurrent newServerWithScrollbackLogger() error = %q, want %q", res.err.Error(), wantErr)
		}
	}
	for _, srv := range winners {
		if srv != nil {
			t.Cleanup(srv.Shutdown)
		}
	}
	if got := len(winners); got != 1 {
		t.Fatalf("successful starts = %d, want 1", got)
	}
}

func startSessionLockHelper(t *testing.T, mode, session string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run=^TestSessionLockHelperProcess$")
	cmd.Env = append(os.Environ(),
		sessionLockHelperModeEnv+"="+mode,
		sessionLockHelperSessionEnv+"="+session,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe(): %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting helper: %v", err)
	}

	reader := bufio.NewReader(stdout)
	ready := make(chan error, 1)
	go func() {
		line, err := reader.ReadString('\n')
		if err != nil {
			ready <- err
			return
		}
		if strings.TrimSpace(line) != "ready" {
			ready <- fmt.Errorf("ready line = %q, want %q", strings.TrimSpace(line), "ready")
			return
		}
		ready <- nil
	}()

	select {
	case err := <-ready:
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("waiting for helper readiness: %v\nstderr:\n%s", err, stderr.String())
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("timed out waiting for helper readiness\nstderr:\n%s", stderr.String())
	}

	return cmd, &stderr
}
