package ipc

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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
