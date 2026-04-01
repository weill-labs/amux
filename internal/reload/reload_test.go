package reload

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestResetDebounceTimerCreatesTimer(t *testing.T) {
	t.Parallel()

	timer := resetDebounceTimer(nil, 20*time.Millisecond)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	select {
	case <-timer.C:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected debounce timer to fire")
	}
}

func TestResetDebounceTimerDrainsExpiredUnreadTimer(t *testing.T) {
	t.Parallel()

	timer := time.NewTimer(20 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)

	timer = resetDebounceTimer(timer, 80*time.Millisecond)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	select {
	case <-timer.C:
		t.Fatal("debounce timer should not fire immediately after reset")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestResetDebounceTimerHandlesExpiredDrainedTimer(t *testing.T) {
	t.Parallel()

	timer := time.NewTimer(20 * time.Millisecond)
	select {
	case <-timer.C:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected timer to expire before reset")
	}

	timer = resetDebounceTimer(timer, 80*time.Millisecond)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	select {
	case <-timer.C:
		t.Fatal("debounce timer should not fire immediately after drained reset")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestWatchEventMatchesTarget(t *testing.T) {
	t.Parallel()

	if !watchEventMatchesTarget(fsnotify.Event{Name: "/tmp/amux", Op: fsnotify.Write}, "amux") {
		t.Fatal("write event for target binary should match")
	}
	if !watchEventMatchesTarget(fsnotify.Event{Name: "/tmp/amux", Op: fsnotify.Create}, "amux") {
		t.Fatal("create event for target binary should match")
	}
	if watchEventMatchesTarget(fsnotify.Event{Name: "/tmp/other", Op: fsnotify.Write}, "amux") {
		t.Fatal("write event for a different file should not match")
	}
	if watchEventMatchesTarget(fsnotify.Event{Name: "/tmp/amux", Op: fsnotify.Remove}, "amux") {
		t.Fatal("non-write/create event for target binary should not match")
	}
}

func TestDrainPendingReloadEvents(t *testing.T) {
	t.Parallel()

	events := make(chan fsnotify.Event, 4)
	errors := make(chan error, 2)
	events <- fsnotify.Event{Name: "/tmp/other", Op: fsnotify.Write}
	events <- fsnotify.Event{Name: "/tmp/amux", Op: fsnotify.Write}
	errors <- nil

	if !drainPendingReloadEvents(events, errors, "amux") {
		t.Fatal("drain should report a matching pending reload event")
	}
	if len(events) != 0 {
		t.Fatalf("drain should consume all pending events, got %d left", len(events))
	}
	if len(errors) != 0 {
		t.Fatalf("drain should consume pending errors, got %d left", len(errors))
	}
}

func TestDrainPendingReloadEventsNoMatch(t *testing.T) {
	t.Parallel()

	events := make(chan fsnotify.Event, 2)
	errors := make(chan error, 1)
	events <- fsnotify.Event{Name: "/tmp/other", Op: fsnotify.Write}
	errors <- nil

	if drainPendingReloadEvents(events, errors, "amux") {
		t.Fatal("drain should ignore unrelated pending events")
	}
}

func TestWatchBinaryDebounce(t *testing.T) {
	t.Parallel()

	// Create a temp directory with a fake binary
	dir := t.TempDir()
	binPath := filepath.Join(dir, "amux-test")
	if err := os.WriteFile(binPath, []byte("v1"), 0755); err != nil {
		t.Fatal(err)
	}

	triggerReload := make(chan struct{}, 1)
	ready := make(chan struct{})
	go WatchBinary(binPath, triggerReload, ready)
	<-ready

	// Write to the file multiple times in quick succession (simulates go build)
	for i := 0; i < 5; i++ {
		os.WriteFile(binPath, []byte("v2"), 0755)
		time.Sleep(20 * time.Millisecond)
	}

	// Should get exactly one trigger after debounce settles
	select {
	case <-triggerReload:
		// Good — got the debounced trigger
	case <-time.After(2 * time.Second):
		t.Fatal("expected reload trigger after debounce, got none")
	}

	// Should NOT get a second trigger (debounce coalesced all writes)
	select {
	case <-triggerReload:
		t.Fatal("got unexpected second reload trigger — debounce failed")
	case <-time.After(500 * time.Millisecond):
		// Good — no extra trigger
	}
}

func TestWatchBinaryIgnoresOtherFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	binPath := filepath.Join(dir, "amux-test")
	otherPath := filepath.Join(dir, "other-file")

	if err := os.WriteFile(binPath, []byte("v1"), 0755); err != nil {
		t.Fatal(err)
	}

	triggerReload := make(chan struct{}, 1)
	ready := make(chan struct{})
	go WatchBinary(binPath, triggerReload, ready)
	<-ready

	// Write to a different file in the same directory
	os.WriteFile(otherPath, []byte("noise"), 0644)
	time.Sleep(500 * time.Millisecond)

	// Should NOT trigger reload
	select {
	case <-triggerReload:
		t.Fatal("reload triggered by unrelated file change")
	case <-time.After(500 * time.Millisecond):
		// Good — ignored
	}
}

func TestWatchBinaryNilReady(t *testing.T) {
	t.Parallel()

	// Passing nil for the ready channel should not panic.
	dir := t.TempDir()
	binPath := filepath.Join(dir, "amux-test")
	if err := os.WriteFile(binPath, []byte("v1"), 0755); err != nil {
		t.Fatal(err)
	}

	triggerReload := make(chan struct{}, 1)
	go WatchBinary(binPath, triggerReload, nil)

	// Inherent race: cannot use ready channel since we're testing nil.
	// Generous 2s fallback timeout below handles slow CI.
	<-time.After(200 * time.Millisecond) // let watcher register
	os.WriteFile(binPath, []byte("v2"), 0755)

	select {
	case <-triggerReload:
		// Good — watcher works with nil ready channel
	case <-time.After(2 * time.Second):
		t.Fatal("expected reload trigger with nil ready channel")
	}
}

func TestWatchBinaryBadDirClosesReady(t *testing.T) {
	t.Parallel()

	// When the directory doesn't exist, watcher.Add fails and ready
	// should still be closed so callers don't block forever.
	ready := make(chan struct{})
	triggerReload := make(chan struct{}, 1)

	go WatchBinary("/nonexistent/path/amux-test", triggerReload, ready)

	select {
	case <-ready:
		// Good — ready was closed despite the error
	case <-time.After(2 * time.Second):
		t.Fatal("ready channel should be closed when watcher.Add fails")
	}
}

func TestWatchBinaryDeleteAndRecreate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	binPath := filepath.Join(dir, "amux-test")

	if err := os.WriteFile(binPath, []byte("v1"), 0755); err != nil {
		t.Fatal(err)
	}

	triggerReload := make(chan struct{}, 1)
	ready := make(chan struct{})
	go WatchBinary(binPath, triggerReload, ready)
	<-ready

	// Delete and recreate (simulates build tools that replace via rename)
	os.Remove(binPath)
	time.Sleep(50 * time.Millisecond)
	os.WriteFile(binPath, []byte("v2"), 0755)

	// Should trigger reload after debounce
	select {
	case <-triggerReload:
		// Good
	case <-time.After(2 * time.Second):
		t.Fatal("expected reload trigger after delete+create, got none")
	}
}
