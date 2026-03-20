package reload

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestShouldWatchBinary_NoInstallMetadata(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "amux-test")
	if err := os.WriteFile(binPath, []byte("v1"), 0755); err != nil {
		t.Fatal(err)
	}

	if !shouldWatchBinary(binPath, dir) {
		t.Fatal("should watch binary without install metadata")
	}
}

func TestShouldWatchBinary_SharedInstallSameRepo(t *testing.T) {
	execDir := t.TempDir()
	binPath := filepath.Join(execDir, "amux")
	if err := os.WriteFile(binPath, []byte("v1"), 0755); err != nil {
		t.Fatal(err)
	}

	repoRoot := filepath.Join(t.TempDir(), "amux9")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath+".install-meta", []byte("source_repo="+repoRoot+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cwd := filepath.Join(repoRoot, "internal", "client")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatal(err)
	}

	if !shouldWatchBinary(binPath, cwd) {
		t.Fatal("should watch binary when cwd is inside source_repo")
	}
}

func TestShouldWatchBinary_SharedInstallDifferentRepo(t *testing.T) {
	execDir := t.TempDir()
	binPath := filepath.Join(execDir, "amux")
	if err := os.WriteFile(binPath, []byte("v1"), 0755); err != nil {
		t.Fatal(err)
	}

	sourceRepo := filepath.Join(t.TempDir(), "amux9")
	if err := os.MkdirAll(filepath.Join(sourceRepo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath+".install-meta", []byte("source_repo="+sourceRepo+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	otherRepo := filepath.Join(t.TempDir(), "amux5")
	if err := os.MkdirAll(filepath.Join(otherRepo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	if shouldWatchBinary(binPath, otherRepo) {
		t.Fatal("should not watch binary when cwd repo differs from source_repo")
	}
}

func TestShouldWatchBinary_SharedInstallOutsideRepo(t *testing.T) {
	execDir := t.TempDir()
	binPath := filepath.Join(execDir, "amux")
	if err := os.WriteFile(binPath, []byte("v1"), 0755); err != nil {
		t.Fatal(err)
	}

	sourceRepo := filepath.Join(t.TempDir(), "amux9")
	if err := os.MkdirAll(filepath.Join(sourceRepo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath+".install-meta", []byte("source_repo="+sourceRepo+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	plainDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(plainDir, 0755); err != nil {
		t.Fatal(err)
	}

	if shouldWatchBinary(binPath, plainDir) {
		t.Fatal("should not watch shared installed binary outside source repo")
	}
}

func TestWatchBinaryDebounce(t *testing.T) {
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
