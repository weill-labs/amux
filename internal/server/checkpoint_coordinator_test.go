package server

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
)

func awaitCheckpointWrite(t *testing.T, writes <-chan string) string {
	t.Helper()

	select {
	case path := <-writes:
		return path
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for checkpoint write")
		return ""
	}
}

func TestCheckpointCoordinatorTriggerDebouncesWrites(t *testing.T) {
	t.Parallel()

	clock := NewFakeClock(time.Date(2026, time.April, 5, 12, 0, 0, 0, time.UTC))
	startedAt := time.Date(2026, time.April, 5, 11, 0, 0, 0, time.UTC)
	writes := make(chan string, 4)

	coord := NewCheckpointCoordinator(CheckpointCoordinatorConfig{
		Clock:       clock,
		Debounce:    500 * time.Millisecond,
		Periodic:    30 * time.Second,
		SessionName: func() string { return "debounce-session" },
		SessionStart: func() time.Time {
			return startedAt
		},
		BuildCrashCheckpoint: func() *checkpoint.CrashCheckpoint {
			return &checkpoint.CrashCheckpoint{Version: checkpoint.CrashVersion}
		},
		WriteCrash: func(cp *checkpoint.CrashCheckpoint, session string, start time.Time) error {
			writes <- session
			return nil
		},
	})
	defer coord.Stop()

	clock.AwaitTimers(1)

	coord.Trigger()
	coord.Trigger()

	clock.AwaitTimers(2)
	clock.Advance(499 * time.Millisecond)

	select {
	case got := <-writes:
		t.Fatalf("checkpoint write before debounce elapsed = %q, want none", got)
	default:
	}

	clock.Advance(1 * time.Millisecond)

	if got := awaitCheckpointWrite(t, writes); got != "debounce-session" {
		t.Fatalf("checkpoint write session = %q, want debounce-session", got)
	}

	select {
	case got := <-writes:
		t.Fatalf("extra checkpoint write after debounce = %q", got)
	default:
	}
}

func TestCheckpointCoordinatorWritesPeriodically(t *testing.T) {
	t.Parallel()

	clock := NewFakeClock(time.Date(2026, time.April, 5, 12, 0, 0, 0, time.UTC))
	startedAt := time.Date(2026, time.April, 5, 11, 0, 0, 0, time.UTC)
	writes := make(chan string, 4)

	coord := NewCheckpointCoordinator(CheckpointCoordinatorConfig{
		Clock:       clock,
		Debounce:    500 * time.Millisecond,
		Periodic:    30 * time.Second,
		SessionName: func() string { return "periodic-session" },
		SessionStart: func() time.Time {
			return startedAt
		},
		BuildCrashCheckpoint: func() *checkpoint.CrashCheckpoint {
			return &checkpoint.CrashCheckpoint{Version: checkpoint.CrashVersion}
		},
		WriteCrash: func(cp *checkpoint.CrashCheckpoint, session string, start time.Time) error {
			writes <- session
			return nil
		},
	})
	defer coord.Stop()

	clock.AwaitTimers(1)

	clock.Advance(30 * time.Second)
	if got := awaitCheckpointWrite(t, writes); got != "periodic-session" {
		t.Fatalf("first periodic checkpoint session = %q, want periodic-session", got)
	}

	clock.AwaitTimers(2)
	clock.Advance(30 * time.Second)
	if got := awaitCheckpointWrite(t, writes); got != "periodic-session" {
		t.Fatalf("second periodic checkpoint session = %q, want periodic-session", got)
	}
}

func TestCheckpointCoordinatorWriteNowWritesCrashCheckpointFile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	startedAt := time.Date(2026, time.April, 5, 11, 0, 0, 0, time.UTC)
	wantPath := checkpoint.CrashCheckpointPathTimestamped("file-session", startedAt)
	wantCheckpoint := &checkpoint.CrashCheckpoint{
		Version:       checkpoint.CrashVersion,
		SessionName:   "file-session",
		Counter:       3,
		WindowCounter: 2,
		Generation:    7,
		Timestamp:     time.Date(2026, time.April, 5, 12, 0, 0, 0, time.UTC),
	}

	var mu sync.Mutex
	var writtenPath string
	var loggedPath string
	var loggedErr error

	coord := NewCheckpointCoordinator(CheckpointCoordinatorConfig{
		SessionName: func() string { return "file-session" },
		SessionStart: func() time.Time {
			return startedAt
		},
		BuildCrashCheckpoint: func() *checkpoint.CrashCheckpoint {
			return wantCheckpoint
		},
		OnCheckpointWritten: func(path string) {
			mu.Lock()
			defer mu.Unlock()
			writtenPath = path
		},
		LogCheckpointWrite: func(path string, duration time.Duration, err error) {
			mu.Lock()
			defer mu.Unlock()
			loggedPath = path
			loggedErr = err
		},
	})
	defer coord.Stop()

	path, err := coord.WriteNow()
	if err != nil {
		t.Fatalf("WriteNow() error = %v", err)
	}
	if path != wantPath {
		t.Fatalf("WriteNow() path = %q, want %q", path, wantPath)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("os.Stat(%q): %v", path, err)
	}

	saved, err := checkpoint.ReadCrash(path)
	if err != nil {
		t.Fatalf("ReadCrash(%q): %v", path, err)
	}
	if saved.SessionName != wantCheckpoint.SessionName {
		t.Fatalf("saved.SessionName = %q, want %q", saved.SessionName, wantCheckpoint.SessionName)
	}
	if saved.Counter != wantCheckpoint.Counter || saved.WindowCounter != wantCheckpoint.WindowCounter || saved.Generation != wantCheckpoint.Generation {
		t.Fatalf("saved checkpoint counters = %#v, want %#v", saved, wantCheckpoint)
	}

	mu.Lock()
	defer mu.Unlock()
	if writtenPath != wantPath {
		t.Fatalf("written checkpoint path = %q, want %q", writtenPath, wantPath)
	}
	if loggedPath != wantPath {
		t.Fatalf("logged checkpoint path = %q, want %q", loggedPath, wantPath)
	}
	if loggedErr != nil {
		t.Fatalf("logged checkpoint error = %v, want nil", loggedErr)
	}
}

func TestCheckpointCoordinatorWriteSkipsWhenShuttingDown(t *testing.T) {
	t.Parallel()

	coord := NewCheckpointCoordinator(CheckpointCoordinatorConfig{
		SessionName:    func() string { return "shutdown-session" },
		SessionStart:   func() time.Time { return time.Date(2026, time.April, 5, 11, 0, 0, 0, time.UTC) },
		IsShuttingDown: func() bool { return true },
		BuildCrashCheckpoint: func() *checkpoint.CrashCheckpoint {
			t.Fatal("BuildCrashCheckpoint called while shutting down")
			return nil
		},
		WriteCrash: func(cp *checkpoint.CrashCheckpoint, session string, start time.Time) error {
			t.Fatal("WriteCrash called while shutting down")
			return nil
		},
	})
	defer coord.Stop()

	coord.Write()
}

func TestCheckpointCoordinatorStopPreventsFutureWrites(t *testing.T) {
	t.Parallel()

	clock := NewFakeClock(time.Date(2026, time.April, 5, 12, 0, 0, 0, time.UTC))
	writes := make(chan string, 1)

	coord := NewCheckpointCoordinator(CheckpointCoordinatorConfig{
		Clock:       clock,
		Debounce:    500 * time.Millisecond,
		Periodic:    30 * time.Second,
		SessionName: func() string { return "stopped-session" },
		SessionStart: func() time.Time {
			return time.Date(2026, time.April, 5, 11, 0, 0, 0, time.UTC)
		},
		BuildCrashCheckpoint: func() *checkpoint.CrashCheckpoint {
			return &checkpoint.CrashCheckpoint{Version: checkpoint.CrashVersion}
		},
		WriteCrash: func(cp *checkpoint.CrashCheckpoint, session string, start time.Time) error {
			writes <- session
			return nil
		},
	})

	clock.AwaitTimers(1)
	coord.Stop()
	coord.Stop()

	coord.Trigger()
	clock.Advance(time.Hour)

	select {
	case got := <-writes:
		t.Fatalf("checkpoint write after Stop() = %q, want none", got)
	default:
	}
}
