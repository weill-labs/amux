package proto

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type stubDaemonCmd struct {
	startErr   error
	releaseErr error
	started    bool
	released   bool
}

func (c *stubDaemonCmd) Start() error {
	c.started = true
	return c.startErr
}

func (c *stubDaemonCmd) Release() error {
	c.released = true
	return c.releaseErr
}

type stubWriteCloser struct {
	closed bool
}

func (w *stubWriteCloser) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w *stubWriteCloser) Close() error {
	w.closed = true
	return nil
}

type stubLockFile struct {
	fd     uintptr
	closed bool
}

func (f *stubLockFile) Close() error {
	f.closed = true
	return nil
}

func (f *stubLockFile) Fd() uintptr {
	return f.fd
}

func TestEnsureDaemonStartsServerOnceUnderConcurrency(t *testing.T) {
	t.Parallel()

	session := "ensure-daemon-race-" + strings.ReplaceAll(t.Name(), "/", "-")
	lockPath := filepath.Join(SocketDir(), session+".start.lock")
	_ = os.Remove(lockPath)
	t.Cleanup(func() { _ = os.Remove(lockPath) })

	var alive atomic.Bool
	var starts atomic.Int32
	started := make(chan struct{}, 1)
	releaseStart := make(chan struct{})

	fns := daemonFns{
		socketAlive: func(string) bool {
			return alive.Load()
		},
		startDaemon: func(string) error {
			starts.Add(1)
			select {
			case started <- struct{}{}:
			default:
			}
			<-releaseStart
			return nil
		},
		waitForSocket: func(string, time.Duration) error {
			alive.Store(true)
			return nil
		},
	}

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- fns.ensureDaemon(session, 250*time.Millisecond)
		}()
	}
	<-started
	close(releaseStart)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("ensureDaemon returned error: %v", err)
		}
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("startDaemon called %d times, want 1", got)
	}
}

func TestEnsureDaemonIgnoresStartErrorWhenSocketComesUp(t *testing.T) {
	t.Parallel()

	var calls int
	fns := daemonFns{
		socketAlive: func(string) bool {
			calls++
			return calls > 1
		},
		startDaemon: func(string) error {
			return errors.New("boom")
		},
		waitForSocket: func(string, time.Duration) error {
			t.Fatal("waitForSocket should not be called when socket appears after start error")
			return nil
		},
	}

	if err := fns.ensureDaemon("main", time.Second); err != nil {
		t.Fatalf("ensureDaemon() = %v, want nil", err)
	}
}

func TestEnsureDaemonWrapsStartErrorWhenSocketStaysDown(t *testing.T) {
	t.Parallel()

	fns := daemonFns{
		socketAlive: func(string) bool { return false },
		startDaemon: func(string) error { return errors.New("boom") },
		waitForSocket: func(string, time.Duration) error {
			t.Fatal("waitForSocket should not be called after start error")
			return nil
		},
	}

	err := fns.ensureDaemon("main", time.Second)
	if err == nil || err.Error() != "starting server: boom" {
		t.Fatalf("ensureDaemon() error = %v, want %q", err, "starting server: boom")
	}
}

func TestStartDaemonWithDeps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "executable error",
			run: func(t *testing.T) {
				err := startDaemonWithDeps("main", startDaemonDeps{
					executable: func() (string, error) { return "", errors.New("boom") },
				})
				if err == nil || err.Error() != "boom" {
					t.Fatalf("startDaemonWithDeps() error = %v, want %q", err, "boom")
				}
			},
		},
		{
			name: "mkdir error",
			run: func(t *testing.T) {
				err := startDaemonWithDeps("main", startDaemonDeps{
					executable: func() (string, error) { return "/tmp/amux", nil },
					mkdirAll:   func(string, os.FileMode) error { return errors.New("mkdir boom") },
				})
				if err == nil || err.Error() != "creating socket dir: mkdir boom" {
					t.Fatalf("startDaemonWithDeps() error = %v, want %q", err, "creating socket dir: mkdir boom")
				}
			},
		},
		{
			name: "open log error",
			run: func(t *testing.T) {
				err := startDaemonWithDeps("main", startDaemonDeps{
					executable: func() (string, error) { return "/tmp/amux", nil },
					mkdirAll:   func(string, os.FileMode) error { return nil },
					openLog:    func(string) (io.WriteCloser, error) { return nil, errors.New("open boom") },
				})
				if err == nil || err.Error() != "opening log: open boom" {
					t.Fatalf("startDaemonWithDeps() error = %v, want %q", err, "opening log: open boom")
				}
			},
		},
		{
			name: "start error closes log",
			run: func(t *testing.T) {
				logFile := &stubWriteCloser{}
				cmd := &stubDaemonCmd{startErr: errors.New("start boom")}
				err := startDaemonWithDeps("main", startDaemonDeps{
					executable: func() (string, error) { return "/tmp/amux", nil },
					mkdirAll:   func(string, os.FileMode) error { return nil },
					openLog:    func(string) (io.WriteCloser, error) { return logFile, nil },
					newCommand: func(string, string, io.Writer) daemonCmd { return cmd },
				})
				if err == nil || err.Error() != "start boom" {
					t.Fatalf("startDaemonWithDeps() error = %v, want %q", err, "start boom")
				}
				if !logFile.closed {
					t.Fatal("log file was not closed on start error")
				}
				if !cmd.started {
					t.Fatal("command was not started")
				}
			},
		},
		{
			name: "success closes log and releases process",
			run: func(t *testing.T) {
				logFile := &stubWriteCloser{}
				cmd := &stubDaemonCmd{}
				err := startDaemonWithDeps("main", startDaemonDeps{
					executable: func() (string, error) { return "/tmp/amux", nil },
					mkdirAll:   func(string, os.FileMode) error { return nil },
					openLog:    func(string) (io.WriteCloser, error) { return logFile, nil },
					newCommand: func(exe, sessionName string, _ io.Writer) daemonCmd {
						if exe != "/tmp/amux" {
							t.Fatalf("exe = %q, want %q", exe, "/tmp/amux")
						}
						if sessionName != "main" {
							t.Fatalf("sessionName = %q, want %q", sessionName, "main")
						}
						return cmd
					},
				})
				if err != nil {
					t.Fatalf("startDaemonWithDeps() = %v, want nil", err)
				}
				if !logFile.closed {
					t.Fatal("log file was not closed on success")
				}
				if !cmd.started || !cmd.released {
					t.Fatalf("command state = started:%v released:%v, want both true", cmd.started, cmd.released)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.run(t)
		})
	}
}

func TestWaitForSocketReturnsTimeoutWhenSocketNeverAppears(t *testing.T) {
	t.Parallel()

	err := WaitForSocket(filepath.Join(t.TempDir(), "missing.sock"), 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "server did not start within") {
		t.Fatalf("WaitForSocket() error = %v, want timeout", err)
	}
}

func TestWithSessionStartupLockWithDepsErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		deps startupLockDeps
		want string
	}{
		{
			name: "mkdir error",
			deps: startupLockDeps{
				mkdirAll: func(string, os.FileMode) error { return errors.New("mkdir boom") },
			},
			want: "creating socket dir: mkdir boom",
		},
		{
			name: "open file error",
			deps: startupLockDeps{
				mkdirAll: func(string, os.FileMode) error { return nil },
				openFile: func(string, int, os.FileMode) (lockFile, error) {
					return nil, errors.New("open boom")
				},
			},
			want: "opening startup lock: open boom",
		},
		{
			name: "flock error",
			deps: startupLockDeps{
				mkdirAll: func(string, os.FileMode) error { return nil },
				openFile: func(string, int, os.FileMode) (lockFile, error) {
					return &stubLockFile{fd: 7}, nil
				},
				flock: func(int, int) error { return errors.New("flock boom") },
			},
			want: "locking startup lock: flock boom",
		},
		{
			name: "unlock error",
			deps: startupLockDeps{
				mkdirAll: func(string, os.FileMode) error { return nil },
				openFile: func(string, int, os.FileMode) (lockFile, error) {
					return &stubLockFile{fd: 7}, nil
				},
				flock: func(_ int, how int) error {
					if how == syscall.LOCK_EX {
						return nil
					}
					return errors.New("unlock boom")
				},
			},
			want: "unlocking startup lock: unlock boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := withSessionStartupLockWithDeps("main", tt.deps, func() error { return nil })
			if err == nil || err.Error() != tt.want {
				t.Fatalf("withSessionStartupLockWithDeps() error = %v, want %q", err, tt.want)
			}
		})
	}
}
