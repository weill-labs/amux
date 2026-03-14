package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchBinaryDebounce(t *testing.T) {
	// Create a temp directory with a fake binary
	dir := t.TempDir()
	binPath := filepath.Join(dir, "amux-test")
	if err := os.WriteFile(binPath, []byte("v1"), 0755); err != nil {
		t.Fatal(err)
	}

	triggerReload := make(chan struct{}, 1)
	go watchBinary(binPath, triggerReload)

	// Give watcher time to start
	time.Sleep(100 * time.Millisecond)

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
	dir := t.TempDir()
	binPath := filepath.Join(dir, "amux-test")
	otherPath := filepath.Join(dir, "other-file")

	if err := os.WriteFile(binPath, []byte("v1"), 0755); err != nil {
		t.Fatal(err)
	}

	triggerReload := make(chan struct{}, 1)
	go watchBinary(binPath, triggerReload)

	time.Sleep(100 * time.Millisecond)

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

func TestWatchBinaryDeleteAndRecreate(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "amux-test")

	if err := os.WriteFile(binPath, []byte("v1"), 0755); err != nil {
		t.Fatal(err)
	}

	triggerReload := make(chan struct{}, 1)
	go watchBinary(binPath, triggerReload)

	time.Sleep(100 * time.Millisecond)

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
