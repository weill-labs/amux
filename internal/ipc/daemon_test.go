package ipc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestSocketDirAndPath(t *testing.T) {
	wantDir := fmt.Sprintf("/tmp/amux-%d", os.Getuid())
	if got := SocketDir(); got != wantDir {
		t.Fatalf("SocketDir() = %q, want %q", got, wantDir)
	}

	if got := SocketPath("session"); got != filepath.Join(wantDir, "session") {
		t.Fatalf("SocketPath() = %q, want %q", got, filepath.Join(wantDir, "session"))
	}
}

func TestStartDaemonReturnsLogOpenErrorForNestedSessionName(t *testing.T) {
	sessionDir := fmt.Sprintf("missing-%d", time.Now().UnixNano())
	sessionName := filepath.Join(sessionDir, "session")
	_ = os.RemoveAll(filepath.Join(SocketDir(), sessionDir))
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(SocketDir(), sessionDir)) })

	err := StartDaemon(sessionName)
	if err == nil {
		t.Fatal("StartDaemon() returned nil, want opening log error")
	}
	if !strings.Contains(err.Error(), "opening log") {
		t.Fatalf("StartDaemon() error = %q, want opening log context", err)
	}
}

func TestStartDaemonWithDeps(t *testing.T) {
	tests := []struct {
		name           string
		executableErr  error
		mkdirErr       error
		openErr        error
		launchErr      error
		wantErr        string
		wantLaunchCall bool
	}{
		{name: "executable error", executableErr: errors.New("no executable"), wantErr: "no executable"},
		{name: "mkdir error", mkdirErr: errors.New("mkdir failed"), wantErr: "creating log dir: mkdir failed"},
		{name: "open error", openErr: errors.New("open failed"), wantErr: "opening log: open failed"},
		{name: "launch error", launchErr: errors.New("launch failed"), wantErr: "launch failed", wantLaunchCall: true},
		{name: "success", wantLaunchCall: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mkdirPath string
			var openPath string
			var launchExe string
			var launchSession string
			var launchCalls int

			err := startDaemonWithDeps(
				"session",
				func() (string, error) {
					if tt.executableErr != nil {
						return "", tt.executableErr
					}
					return "/tmp/amux-test-binary", nil
				},
				func(path string, perm os.FileMode) error {
					mkdirPath = path
					return tt.mkdirErr
				},
				func(path string) (*os.File, error) {
					openPath = path
					if tt.openErr != nil {
						return nil, tt.openErr
					}
					return os.CreateTemp(t.TempDir(), "daemon-log")
				},
				func(exe, sessionName string, logFile *os.File) error {
					launchCalls++
					launchExe = exe
					launchSession = sessionName
					if logFile == nil {
						t.Fatal("launch() received nil log file")
					}
					return tt.launchErr
				},
			)

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("startDaemonWithDeps() error = %v, want nil", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("startDaemonWithDeps() error = %v, want substring %q", err, tt.wantErr)
			}

			if tt.executableErr == nil && mkdirPath != SocketDir() {
				t.Fatalf("mkdir path = %q, want %q", mkdirPath, SocketDir())
			}
			if tt.executableErr == nil && tt.mkdirErr == nil {
				wantOpenPath := filepath.Join(SocketDir(), "session.log")
				if openPath != wantOpenPath {
					t.Fatalf("open path = %q, want %q", openPath, wantOpenPath)
				}
			}
			if got := launchCalls > 0; got != tt.wantLaunchCall {
				t.Fatalf("launch called = %t, want %t", got, tt.wantLaunchCall)
			}
			if tt.wantLaunchCall {
				if launchExe != "/tmp/amux-test-binary" {
					t.Fatalf("launch exe = %q, want %q", launchExe, "/tmp/amux-test-binary")
				}
				if launchSession != "session" {
					t.Fatalf("launch session = %q, want %q", launchSession, "session")
				}
			}
		})
	}
}

func TestLaunchDaemonProcess(t *testing.T) {
	t.Run("start error", func(t *testing.T) {
		logFile, err := os.CreateTemp(t.TempDir(), "daemon-log")
		if err != nil {
			t.Fatalf("CreateTemp(): %v", err)
		}
		defer logFile.Close()

		err = launchDaemonProcess(
			func(name string, args ...string) *exec.Cmd {
				return exec.Command(filepath.Join(t.TempDir(), "missing-binary"))
			},
			"/tmp/amux-test-binary",
			"session",
			logFile,
		)
		if err == nil {
			t.Fatal("launchDaemonProcess() returned nil, want start error")
		}
	})

	t.Run("success", func(t *testing.T) {
		logFile, err := os.CreateTemp(t.TempDir(), "daemon-log")
		if err != nil {
			t.Fatalf("CreateTemp(): %v", err)
		}
		defer logFile.Close()

		err = launchDaemonProcess(
			func(name string, args ...string) *exec.Cmd {
				cmd := exec.Command(os.Args[0], "-test.run=TestLaunchDaemonProcessHelper", "--")
				cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
				return cmd
			},
			"/tmp/amux-test-binary",
			"session",
			logFile,
		)
		if err != nil {
			t.Fatalf("launchDaemonProcess() error = %v, want nil", err)
		}
	})
}

func TestLaunchDaemonProcessHelper(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	os.Exit(0)
}

func TestEnsureDaemonStartsServerOnceUnderConcurrency(t *testing.T) {
	session := "ensure-daemon-race-test"
	lockPath := filepath.Join(SocketDir(), session+".start.lock")
	_ = os.Remove(lockPath)
	t.Cleanup(func() { _ = os.Remove(lockPath) })

	origSocketAlive := socketAliveFn
	origStartDaemon := startDaemonFn
	origWaitForSocket := waitForSocketFn
	defer func() {
		socketAliveFn = origSocketAlive
		startDaemonFn = origStartDaemon
		waitForSocketFn = origWaitForSocket
	}()

	var alive atomic.Bool
	var starts atomic.Int32
	var mu sync.Mutex
	started := make(chan struct{}, 1)
	releaseStart := make(chan struct{})

	socketAliveFn = func(sockPath string) bool {
		return alive.Load()
	}
	startDaemonFn = func(sessionName string) error {
		starts.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-releaseStart
		return nil
	}
	waitForSocketFn = func(sockPath string, timeout time.Duration) error {
		mu.Lock()
		alive.Store(true)
		mu.Unlock()
		return nil
	}

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- EnsureDaemon(session, 250*time.Millisecond)
		}()
	}
	<-started
	close(releaseStart)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("EnsureDaemon returned error: %v", err)
		}
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("startDaemon called %d times, want 1", got)
	}
}

func TestEnsureDaemonHandlesBootstrapOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		aliveSeq   []bool
		startErr   error
		waitErr    error
		wantErr    string
		wantStarts int
		wantWaits  int
	}{
		{
			name:       "socket already alive",
			aliveSeq:   []bool{true},
			wantStarts: 0,
			wantWaits:  0,
		},
		{
			name:       "start error after another caller made socket live",
			aliveSeq:   []bool{false, true},
			startErr:   errors.New("boom"),
			wantStarts: 1,
			wantWaits:  0,
		},
		{
			name:       "start error while socket stays down",
			aliveSeq:   []bool{false, false},
			startErr:   errors.New("boom"),
			wantErr:    "starting server: boom",
			wantStarts: 1,
			wantWaits:  0,
		},
		{
			name:       "wait error",
			aliveSeq:   []bool{false},
			waitErr:    errors.New("socket timeout"),
			wantErr:    "socket timeout",
			wantStarts: 1,
			wantWaits:  1,
		},
		{
			name:       "wait success",
			aliveSeq:   []bool{false},
			wantStarts: 1,
			wantWaits:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
			lockPath := filepath.Join(SocketDir(), session+".start.lock")
			_ = os.Remove(lockPath)
			t.Cleanup(func() { _ = os.Remove(lockPath) })

			origSocketAlive := socketAliveFn
			origStartDaemon := startDaemonFn
			origWaitForSocket := waitForSocketFn
			defer func() {
				socketAliveFn = origSocketAlive
				startDaemonFn = origStartDaemon
				waitForSocketFn = origWaitForSocket
			}()

			var aliveCalls int
			var startCalls int
			var waitCalls int
			socketAliveFn = func(sockPath string) bool {
				if want := SocketPath(session); sockPath != want {
					t.Fatalf("socketAliveFn path = %q, want %q", sockPath, want)
				}
				aliveCalls++
				if len(tt.aliveSeq) == 0 {
					return false
				}
				idx := aliveCalls - 1
				if idx >= len(tt.aliveSeq) {
					return tt.aliveSeq[len(tt.aliveSeq)-1]
				}
				return tt.aliveSeq[idx]
			}
			startDaemonFn = func(sessionName string) error {
				startCalls++
				if sessionName != session {
					t.Fatalf("startDaemonFn session = %q, want %q", sessionName, session)
				}
				return tt.startErr
			}
			waitForSocketFn = func(sockPath string, timeout time.Duration) error {
				waitCalls++
				if want := SocketPath(session); sockPath != want {
					t.Fatalf("waitForSocketFn path = %q, want %q", sockPath, want)
				}
				return tt.waitErr
			}

			err := EnsureDaemon(session, 250*time.Millisecond)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("EnsureDaemon() error = %v, want nil", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("EnsureDaemon() error = %v, want substring %q", err, tt.wantErr)
			}
			if startCalls != tt.wantStarts {
				t.Fatalf("startDaemonFn calls = %d, want %d", startCalls, tt.wantStarts)
			}
			if waitCalls != tt.wantWaits {
				t.Fatalf("waitForSocketFn calls = %d, want %d", waitCalls, tt.wantWaits)
			}
		})
	}
}

func TestSocketAliveReportsListenerState(t *testing.T) {
	sockPath := SocketPath(fmt.Sprintf("alive-%d", time.Now().UnixNano()))
	_ = os.Remove(sockPath)
	t.Cleanup(func() { _ = os.Remove(sockPath) })
	if SocketAlive(sockPath) {
		t.Fatalf("SocketAlive(%q) = true before listener starts", sockPath)
	}

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Listen(%q): %v", sockPath, err)
	}
	if !SocketAlive(sockPath) {
		ln.Close()
		t.Fatalf("SocketAlive(%q) = false while listener is live", sockPath)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("Close(%q): %v", sockPath, err)
	}
	if SocketAlive(sockPath) {
		t.Fatalf("SocketAlive(%q) = true after listener closes", sockPath)
	}
}

func TestWaitForSocket(t *testing.T) {
	t.Run("returns when listener appears", func(t *testing.T) {
		sockPath := SocketPath(fmt.Sprintf("wait-live-%d", time.Now().UnixNano()))
		_ = os.Remove(sockPath)
		t.Cleanup(func() { _ = os.Remove(sockPath) })
		listenerCh := make(chan net.Listener, 1)
		errCh := make(chan error, 1)

		go func() {
			time.Sleep(25 * time.Millisecond)
			ln, err := net.Listen("unix", sockPath)
			if err != nil {
				errCh <- err
				return
			}
			listenerCh <- ln
		}()

		if err := WaitForSocket(sockPath, time.Second); err != nil {
			t.Fatalf("WaitForSocket(%q) error = %v, want nil", sockPath, err)
		}

		select {
		case err := <-errCh:
			t.Fatalf("Listen(%q): %v", sockPath, err)
		case ln := <-listenerCh:
			defer ln.Close()
		}
	})

	t.Run("times out when socket never appears", func(t *testing.T) {
		sockPath := SocketPath(fmt.Sprintf("wait-missing-%d", time.Now().UnixNano()))
		_ = os.Remove(sockPath)
		t.Cleanup(func() { _ = os.Remove(sockPath) })
		err := WaitForSocket(sockPath, 75*time.Millisecond)
		if err == nil {
			t.Fatalf("WaitForSocket(%q) returned nil, want timeout error", sockPath)
		}
		if !strings.Contains(err.Error(), "server did not start within") {
			t.Fatalf("WaitForSocket(%q) error = %q, want timeout context", sockPath, err)
		}
	})
}

func TestWithSessionStartupLockWithDeps(t *testing.T) {
	tests := []struct {
		name         string
		mkdirErr     error
		openErr      error
		lockErr      error
		fnErr        error
		wantErr      string
		wantFnCalled bool
		wantFlocks   []int
	}{
		{name: "mkdir error", mkdirErr: errors.New("mkdir failed"), wantErr: "creating socket dir: mkdir failed"},
		{name: "open error", openErr: errors.New("open failed"), wantErr: "opening startup lock: open failed"},
		{name: "lock error", lockErr: errors.New("lock failed"), wantErr: "locking startup lock: lock failed", wantFlocks: []int{syscall.LOCK_EX}},
		{name: "callback error", fnErr: errors.New("callback failed"), wantErr: "callback failed", wantFnCalled: true, wantFlocks: []int{syscall.LOCK_EX, syscall.LOCK_UN}},
		{name: "success", wantFnCalled: true, wantFlocks: []int{syscall.LOCK_EX, syscall.LOCK_UN}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mkdirPath string
			var openPath string
			var flocks []int
			fnCalled := false

			err := withSessionStartupLockWithDeps(
				"session",
				func(path string, perm os.FileMode) error {
					mkdirPath = path
					return tt.mkdirErr
				},
				func(path string, flag int, perm os.FileMode) (*os.File, error) {
					openPath = path
					if tt.openErr != nil {
						return nil, tt.openErr
					}
					return os.CreateTemp(t.TempDir(), "startup-lock")
				},
				func(fd int, op int) error {
					flocks = append(flocks, op)
					if op == syscall.LOCK_EX {
						return tt.lockErr
					}
					return nil
				},
				func() error {
					fnCalled = true
					if !tt.wantFnCalled {
						t.Fatal("fn() ran unexpectedly")
					}
					return tt.fnErr
				},
			)

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("withSessionStartupLockWithDeps() error = %v, want nil", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("withSessionStartupLockWithDeps() error = %v, want substring %q", err, tt.wantErr)
			}
			if mkdirPath != SocketDir() {
				t.Fatalf("mkdir path = %q, want %q", mkdirPath, SocketDir())
			}
			if tt.mkdirErr == nil {
				wantOpenPath := filepath.Join(SocketDir(), "session.start.lock")
				if openPath != wantOpenPath {
					t.Fatalf("open path = %q, want %q", openPath, wantOpenPath)
				}
			}
			if fmt.Sprint(flocks) != fmt.Sprint(tt.wantFlocks) {
				t.Fatalf("flock ops = %v, want %v", flocks, tt.wantFlocks)
			}
			if fnCalled != tt.wantFnCalled {
				t.Fatalf("fn called = %t, want %t", fnCalled, tt.wantFnCalled)
			}
		})
	}
}
