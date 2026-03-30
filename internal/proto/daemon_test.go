package proto

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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
