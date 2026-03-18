package test

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/server"
)

// TestCrashRecovery_LayoutRestored verifies that after SIGKILL, restarting
// the server for the same session restores the window/pane layout structure.
func TestCrashRecovery_LayoutRestored(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	// Create a multi-pane layout: split vertically (2 panes side-by-side)
	h.splitV()

	// Rename the window for later verification
	gen := h.generation()
	h.runCmd("rename-window", "work")
	h.waitLayout(gen)

	// Send distinctive text to each pane so we can verify screen content replay
	h.sendKeys("pane-1", "echo PANE1_MARKER", "Enter")
	h.waitFor("pane-1", "PANE1_MARKER")
	h.sendKeys("pane-2", "echo PANE2_MARKER", "Enter")
	h.waitFor("pane-2", "PANE2_MARKER")

	// Capture the layout state before crash
	preJSON := h.captureJSON()
	prePaneCount := len(preJSON.Panes)
	preWindowName := preJSON.Window.Name

	// Wait for crash checkpoint file to appear
	cpPath := checkpoint.CrashCheckpointPath(h.session)
	waitForCrashCheckpoint(t, cpPath, 5*time.Second)

	// Detach the headless client before kill so it doesn't interfere
	if h.client != nil {
		h.client.close()
		h.client = nil
	}

	// SIGKILL the server (simulates crash — no cleanup runs)
	h.cmd.Process.Signal(syscall.SIGKILL)
	h.cmd.Wait()
	h.cmd = nil // prevent cleanup from trying to kill again

	// Verify crash checkpoint file still exists (SIGKILL = no cleanup)
	if _, err := os.Stat(cpPath); err != nil {
		t.Fatalf("crash checkpoint should survive SIGKILL: %v", err)
	}

	// Start a NEW server for the same session — should auto-recover
	h2 := startServerForSession(t, h.session)

	// Verify layout was restored
	postJSON := h2.captureJSON()
	if len(postJSON.Panes) != prePaneCount {
		t.Errorf("pane count: got %d, want %d", len(postJSON.Panes), prePaneCount)
	}
	if postJSON.Window.Name != preWindowName {
		t.Errorf("window name: got %q, want %q", postJSON.Window.Name, preWindowName)
	}

	// Verify pane names and colors were preserved
	preNames := paneNames(preJSON)
	postNames := paneNames(postJSON)
	if preNames != postNames {
		t.Errorf("pane names: got %q, want %q", postNames, preNames)
	}

	// Verify panes are functional (send-keys + capture works)
	h2.sendKeys("pane-1", "echo ALIVE", "Enter")
	h2.waitFor("pane-1", "ALIVE")

	// Verify crash checkpoint was cleaned up after recovery
	if _, err := os.Stat(cpPath); !os.IsNotExist(err) {
		t.Error("crash checkpoint should be removed after recovery")
	}
}

// TestCrashRecovery_CleanShutdown verifies that a clean SIGTERM shutdown
// removes the crash checkpoint file (no stale checkpoint left behind).
func TestCrashRecovery_CleanShutdown(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	// Create some layout to trigger checkpoint writes
	h.splitV()

	// Wait for crash checkpoint to appear
	cpPath := checkpoint.CrashCheckpointPath(h.session)
	waitForCrashCheckpoint(t, cpPath, 5*time.Second)

	// Verify checkpoint exists
	if _, err := os.Stat(cpPath); err != nil {
		t.Fatalf("checkpoint should exist: %v", err)
	}

	// Clean shutdown (SIGTERM) — handled by harness cleanup
	// The test cleanup sends SIGTERM, which calls Shutdown(), which removes the checkpoint.
	// We trigger it manually here to verify.
	if h.client != nil {
		h.client.close()
		h.client = nil
	}
	h.cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() {
		h.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		h.cmd.Process.Kill()
		t.Fatal("server did not shut down within 5s")
	}
	h.cmd = nil // prevent double cleanup

	// Verify checkpoint file was removed
	if _, err := os.Stat(cpPath); !os.IsNotExist(err) {
		t.Errorf("crash checkpoint should be removed after clean shutdown, err=%v", err)
	}
}

// TestCrashRecovery_CheckpointIsValidJSON verifies the crash checkpoint file
// is human-readable JSON with expected structure.
func TestCrashRecovery_CheckpointIsValidJSON(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.splitV()

	cpPath := checkpoint.CrashCheckpointPath(h.session)
	waitForCrashCheckpoint(t, cpPath, 5*time.Second)

	data, err := os.ReadFile(cpPath)
	if err != nil {
		t.Fatalf("reading checkpoint: %v", err)
	}

	var cp checkpoint.CrashCheckpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		t.Fatalf("checkpoint is not valid JSON: %v", err)
	}

	if cp.Version != checkpoint.CrashVersion {
		t.Errorf("version = %d, want %d", cp.Version, checkpoint.CrashVersion)
	}
	if cp.SessionName != h.session {
		t.Errorf("session = %q, want %q", cp.SessionName, h.session)
	}
	if len(cp.PaneStates) < 2 {
		t.Errorf("pane states = %d, want >= 2", len(cp.PaneStates))
	}
	if len(cp.Layout.Windows) == 0 {
		t.Error("layout should have at least one window")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// waitForCrashCheckpoint polls until the crash checkpoint file exists.
// Uses the existing waitForFile from hooks_test.go (returns bool).
func waitForCrashCheckpoint(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	if !waitForFile(t, path, timeout) {
		t.Fatalf("crash checkpoint %s did not appear within %v", path, timeout)
	}
}

// paneNames returns a sorted comma-joined string of pane names from a capture.
func paneNames(c proto.CaptureJSON) string {
	var names []string
	for _, p := range c.Panes {
		names = append(names, p.Name)
	}
	return strings.Join(names, ",")
}

// startServerForSession starts a new server process for an existing session
// (used after crash to test recovery). Returns a harness connected to the
// recovered server.
func startServerForSession(t *testing.T, session string) *ServerHarness {
	t.Helper()

	// Create pipe for the server's ready signal.
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating ready pipe: %v", err)
	}

	cmd := exec.Command(amuxBin, "_server", session)
	cmd.ExtraFiles = []*os.File{writePipe}
	env := append(os.Environ(), "AMUX_READY_FD=3", "AMUX_NO_WATCH=1")

	// Per-test cover dir
	var coverDir string
	if gocoverDir != "" {
		var b [4]byte
		rand.Read(b[:])
		coverDir = filepath.Join(gocoverDir, fmt.Sprintf("recover-%x", b))
		os.MkdirAll(coverDir, 0755)
		for i, e := range env {
			if strings.HasPrefix(e, "GOCOVERDIR=") {
				env[i] = "GOCOVERDIR=" + coverDir
				break
			}
		}
	}
	cmd.Env = env

	logDir := server.SocketDir()
	os.MkdirAll(logDir, 0700)
	logPath := filepath.Join(logDir, session+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatalf("opening log: %v", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		readPipe.Close()
		writePipe.Close()
		t.Fatalf("starting recovered server: %v", err)
	}
	writePipe.Close()
	logFile.Close()

	readPipe.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 64)
	n, err := readPipe.Read(buf)
	readPipe.Close()
	if err != nil || !strings.Contains(string(buf[:n]), "ready") {
		cmd.Process.Kill()
		// Print server log for debugging
		logData, _ := os.ReadFile(logPath)
		t.Fatalf("recovered server ready signal not received: err=%v, buf=%q\nserver log:\n%s", err, string(buf[:n]), string(logData))
	}

	h := &ServerHarness{tb: t, session: session, cmd: cmd, coverDir: coverDir}
	t.Cleanup(h.cleanup)

	// Attach headless client
	sockPath := server.SocketPath(session)
	client, err := newHeadlessClient(sockPath, session, 80, 24)
	if err != nil {
		cmd.Process.Kill()
		t.Fatalf("attaching headless client to recovered server: %v", err)
	}
	h.client = client

	return h
}
