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

	"github.com/fsnotify/fsnotify"
	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/server"
)

// TestCrashRecovery_LayoutRestored verifies that after SIGKILL, restarting
// the server for the same session restores the window/pane layout structure.
func TestCrashRecovery_LayoutRestored(t *testing.T) {
	t.Parallel()

	// Use persistent server: the test deliberately disconnects the client
	// before SIGKILL, so exit-unattached would shut down the server early.
	h := newServerHarnessPersistent(t)

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
	preNames := paneNames(preJSON)

	cpPath := waitForCrashCheckpointPath(t, h.home, h.session, 5*time.Second)
	_ = waitForCrashCheckpointMatch(t, cpPath, 5*time.Second, "checkpoint with split layout and renamed window", func(cp checkpoint.CrashCheckpoint) bool {
		return crashCheckpointWindowName(cp) == preWindowName &&
			len(cp.PaneStates) == prePaneCount &&
			crashCheckpointPaneContains(cp, "pane-1", "PANE1_MARKER") &&
			crashCheckpointPaneContains(cp, "pane-2", "PANE2_MARKER")
	})
	preCrashCP := readCrashCheckpoint(t, cpPath)

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
	h2 := startServerForSession(t, h.session, h.home)

	// Verify layout was restored
	postJSON := h2.captureJSON()
	if len(postJSON.Panes) != prePaneCount {
		t.Errorf("pane count: got %d, want %d", len(postJSON.Panes), prePaneCount)
	}
	if postJSON.Window.Name != preWindowName {
		t.Errorf("window name: got %q, want %q", postJSON.Window.Name, preWindowName)
	}

	// Verify pane names and colors were preserved
	postNames := paneNames(postJSON)
	if preNames != postNames {
		t.Errorf("pane names: got %q, want %q", postNames, preNames)
	}

	// Verify panes are functional (send-keys + capture works)
	h2.sendKeys("pane-1", "echo ALIVE", "Enter")
	h2.waitFor("pane-1", "ALIVE")

	// Recovery removes the stale checkpoint before signaling ready, but the
	// recovered server immediately resumes normal crash checkpointing on the
	// next layout broadcast (for example when the client reattaches). The
	// durable invariant is that recovery replaces the stale checkpoint with a
	// fresh one from the recovered session rather than relying on the pre-crash
	// file indefinitely.
	recoveredPath := crashCheckpointPathTimestamped(h.home, h.session, preCrashCP.Timestamp)
	postCrashCP := waitForFreshCrashCheckpoint(t, recoveredPath, preCrashCP, 5*time.Second)
	if postCrashCP.SessionName != h.session {
		t.Errorf("refreshed crash checkpoint session = %q, want %q", postCrashCP.SessionName, h.session)
	}
}

// TestCrashRecovery_CleanShutdown verifies that a clean shutdown
// removes the crash checkpoint file (no stale checkpoint left behind).
func TestCrashRecovery_CleanShutdown(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	// Create some layout to trigger checkpoint writes
	h.splitV()

	// Wait for crash checkpoint to appear
	cpPath := waitForCrashCheckpointPath(t, h.home, h.session, 5*time.Second)

	// Verify checkpoint exists
	if _, err := os.Stat(cpPath); err != nil {
		t.Fatalf("checkpoint should exist: %v", err)
	}

	// Trigger a clean shutdown explicitly and wait for the server's
	// shutdown-complete signal before asserting on filesystem cleanup.
	if h.client != nil {
		h.client.close()
		h.client = nil
	}
	h.cmd.Process.Signal(os.Interrupt)
	h.waitForShutdownSignal(5 * time.Second)
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
		t.Fatalf("crash checkpoint %s should be removed after clean shutdown, err=%v", cpPath, err)
	}
}

// TestCrashRecovery_CheckpointIsValidJSON verifies the crash checkpoint file
// is human-readable JSON with expected structure.
func TestCrashRecovery_CheckpointIsValidJSON(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.splitV()

	cpPath := waitForCrashCheckpointPath(t, h.home, h.session, 5*time.Second)

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

func TestCrashRecovery_FocusUpFromRestoredFullWidthBottomPane(t *testing.T) {
	t.Parallel()

	h := newServerHarnessPersistent(t)

	makeThreeByThreeGridServer(t, h)
	h.doFocus("pane-9")
	h.doSplit("root")
	h.assertActive("pane-10")

	cpPath := waitForCrashCheckpointPath(t, h.home, h.session, 5*time.Second)
	_ = waitForCrashCheckpointMatch(t, cpPath, 5*time.Second, "checkpoint with pane-10 active", func(cp checkpoint.CrashCheckpoint) bool {
		return len(cp.PaneStates) == 10 &&
			crashCheckpointPaneNamed(cp, "pane-10") &&
			crashCheckpointActivePaneName(cp) == "pane-10"
	})

	if h.client != nil {
		h.client.close()
		h.client = nil
	}
	h.cmd.Process.Signal(syscall.SIGKILL)
	h.cmd.Wait()
	h.cmd = nil

	h2 := startServerForSession(t, h.session, h.home)
	h2.assertActive("pane-10")

	out := h2.runCmd("focus", "up")
	if strings.Contains(out, "Focused pane-10") {
		t.Fatalf("focus up after crash recovery should move to a pane above, got output %q\ncapture:\n%s", strings.TrimSpace(out), h2.capture())
	}
}

func TestCrashRecovery_PreservesHistoryCapture(t *testing.T) {
	t.Parallel()

	h := newServerHarnessPersistent(t)
	cpPath := waitForCrashCheckpointPath(t, h.home, h.session, 5*time.Second)
	preCrashCP := readCrashCheckpoint(t, cpPath)

	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-crash-history-%s.sh", h.session))
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\nfor i in $(seq -w 1 45); do echo \"CRASHHIST-$i\"; done\n"), 0755); err != nil {
		t.Fatalf("writing history script: %v", err)
	}
	t.Cleanup(func() { os.Remove(scriptPath) })

	h.sendKeys("pane-1", scriptPath, "Enter")
	before := waitForHistoryCaptureContains(t, h, "pane-1", "CRASHHIST-45", 10*time.Second)
	if !strings.Contains(before, "CRASHHIST-01") {
		t.Fatalf("history capture before crash should include earliest retained line, got:\n%s", before)
	}

	_ = waitForCrashCheckpointPaneContains(t, cpPath, "pane-1", preCrashCP, 5*time.Second, "CRASHHIST-01", "CRASHHIST-45")

	if h.client != nil {
		h.client.close()
		h.client = nil
	}
	h.cmd.Process.Signal(syscall.SIGKILL)
	h.cmd.Wait()
	h.cmd = nil

	h2 := startServerForSession(t, h.session, h.home)

	after := h2.runCmd("capture", "--history", "pane-1")
	if !strings.Contains(after, "CRASHHIST-01") || !strings.Contains(after, "CRASHHIST-45") {
		t.Fatalf("history capture should survive crash recovery, got:\n%s", after)
	}
}

func TestCrashRecovery_ReplaysVisibleScreenForIdleShellPane(t *testing.T) {
	t.Parallel()

	h := newServerHarnessPersistent(t)
	cpPath := waitForCrashCheckpointPath(t, h.home, h.session, 5*time.Second)

	h.sendKeys("pane-1", `printf 'IDLE_SCREEN_MARKER\n'`, "Enter")
	h.waitFor("pane-1", "IDLE_SCREEN_MARKER")
	_ = waitForCrashCheckpointMatch(t, cpPath, 5*time.Second, "checkpoint containing idle screen marker", func(cp checkpoint.CrashCheckpoint) bool {
		return crashCheckpointPaneContains(cp, "pane-1", "IDLE_SCREEN_MARKER")
	})

	if h.client != nil {
		h.client.close()
		h.client = nil
	}
	h.cmd.Process.Signal(syscall.SIGKILL)
	h.cmd.Wait()
	h.cmd = nil

	h2 := startServerForSession(t, h.session, h.home)

	out := h2.runCmd("capture", "pane-1")
	if !strings.Contains(out, "IDLE_SCREEN_MARKER") {
		t.Fatalf("idle shell pane should replay visible screen after crash recovery, got:\n%s", out)
	}
}

func TestCrashRecovery_BusyPaneShowsRecoveryNoticeInsteadOfReplayingStaleScreen(t *testing.T) {
	t.Parallel()

	h := newServerHarnessPersistent(t)
	cpPath := waitForCrashCheckpointPath(t, h.home, h.session, 5*time.Second)

	h.sendKeys("pane-1", `printf '\033[2J\033[HCRASH_BUSY_FRAME\n'; while true; do sleep 1; printf '\033[0m'; done`, "Enter")
	h.waitFor("pane-1", "CRASH_BUSY_FRAME")
	h.waitBusy("pane-1")
	_ = waitForCrashCheckpointMatch(t, cpPath, 5*time.Second, "checkpoint containing busy frame", func(cp checkpoint.CrashCheckpoint) bool {
		return crashCheckpointPaneContains(cp, "pane-1", "CRASH_BUSY_FRAME") && !crashCheckpointPaneWasIdle(cp, "pane-1")
	})

	if h.client != nil {
		h.client.close()
		h.client = nil
	}
	h.cmd.Process.Signal(syscall.SIGKILL)
	h.cmd.Wait()
	h.cmd = nil

	h2 := startServerForSession(t, h.session, h.home)

	paneOut := h2.runCmd("capture", "pane-1")
	if strings.Contains(paneOut, "CRASH_BUSY_FRAME") {
		t.Fatalf("busy pane should not replay stale visible screen after crash recovery, got:\n%s", paneOut)
	}
	if !strings.Contains(paneOut, "previous process lost during crash recovery") {
		t.Fatalf("busy pane should show crash-recovery notice, got:\n%s", paneOut)
	}

	historyOut := h2.runCmd("capture", "--history", "pane-1")
	if !strings.Contains(historyOut, "amux: archived pre-crash visible screen") {
		t.Fatalf("history capture should include archived pre-crash marker, got:\n%s", historyOut)
	}
	if !strings.Contains(historyOut, "CRASH_BUSY_FRAME") {
		t.Fatalf("history capture should preserve archived pre-crash visible output, got:\n%s", historyOut)
	}
}

func TestWaitForCrashCheckpointPathSeesAtomicRenameNearTimeout(t *testing.T) {
	t.Parallel()

	home := newTestHome(t)
	session := "rename-checkpoint"
	startTime := time.Date(2026, time.March, 25, 12, 34, 56, 0, time.UTC)
	dest := crashCheckpointPathTimestamped(home, session, startTime)

	writeDone := make(chan error, 1)
	go func() {
		// Land just after the old helper's 50ms poll, but still before timeout.
		// The watcher-based helper should still see the rename immediately.
		delay := time.NewTimer(55 * time.Millisecond)
		defer delay.Stop()
		<-delay.C

		tmp, err := os.CreateTemp(crashCheckpointDir(home), ".crash-*.json.tmp")
		if err != nil {
			writeDone <- fmt.Errorf("create temp checkpoint: %w", err)
			return
		}
		tmpPath := tmp.Name()
		if _, err := tmp.WriteString(`{"version":1}`); err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			writeDone <- fmt.Errorf("write temp checkpoint: %w", err)
			return
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmpPath)
			writeDone <- fmt.Errorf("close temp checkpoint: %w", err)
			return
		}
		if err := os.Rename(tmpPath, dest); err != nil {
			os.Remove(tmpPath)
			writeDone <- fmt.Errorf("rename temp checkpoint: %w", err)
			return
		}

		writeDone <- nil
	}()

	got := waitForCrashCheckpointPath(t, home, session, 99*time.Millisecond)
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if got != dest {
		t.Fatalf("waitForCrashCheckpointPath() = %q, want %q", got, dest)
	}
}

func makeThreeByThreeGridServer(t *testing.T, h *ServerHarness) {
	t.Helper()

	h.doSplit("root", "v")
	h.doSplit("root", "v")

	for _, pane := range []string{"pane-1", "pane-2", "pane-3"} {
		h.doFocus(pane)
		h.doSplit()
		h.doSplit()
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newestCrashCheckpointPath(home, session string) string {
	checkpointDir := crashCheckpointDir(home)
	suffix := "_" + session + ".json"

	entries, err := os.ReadDir(checkpointDir)
	if err != nil {
		return ""
	}

	var newest string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, suffix) {
			continue
		}
		path := filepath.Join(checkpointDir, name)
		if newest == "" || path > newest {
			newest = path
		}
	}
	return newest
}

// waitForCrashCheckpointPath waits until the newest crash checkpoint path for
// the session appears. Crash checkpoints are written with a temp file + rename,
// so watch the directory for changes and periodically rescan as a fallback.
func waitForCrashCheckpointPath(t *testing.T, home, session string, timeout time.Duration) string {
	t.Helper()

	checkpointDir := crashCheckpointDir(home)
	if newest := newestCrashCheckpointPath(home, session); newest != "" {
		return newest
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	rescan := time.NewTicker(250 * time.Millisecond)
	defer rescan.Stop()

	var watchEvents <-chan fsnotify.Event
	var watchErrors <-chan error
	watcher, err := fsnotify.NewWatcher()
	if err == nil {
		defer watcher.Close()
		if err := watcher.Add(checkpointDir); err == nil {
			if newest := newestCrashCheckpointPath(home, session); newest != "" {
				return newest
			}
			watchEvents = watcher.Events
			watchErrors = watcher.Errors
		}
	}

	for {
		select {
		case event, ok := <-watchEvents:
			if !ok {
				watchEvents = nil
				watchErrors = nil
				continue
			}
			if event.Op&(fsnotify.Create|fsnotify.Rename|fsnotify.Write) == 0 {
				continue
			}
			if newest := newestCrashCheckpointPath(home, session); newest != "" {
				return newest
			}
		case _, ok := <-watchErrors:
			if !ok {
				watchEvents = nil
				watchErrors = nil
			}
		case <-rescan.C:
			if newest := newestCrashCheckpointPath(home, session); newest != "" {
				return newest
			}
		case <-timer.C:
			t.Fatalf("crash checkpoint for session %s in %s did not appear within %v", session, checkpointDir, timeout)
		}
	}
}

func crashCheckpointPathTimestamped(home, session string, startTime time.Time) string {
	return filepath.Join(crashCheckpointDir(home), startTime.Format("20060102-150405")+"_"+session+".json")
}

func crashCheckpointDir(home string) string {
	return filepath.Join(home, ".local", "state", "amux")
}

func waitForCrashCheckpointMatch(t *testing.T, path string, timeout time.Duration, desc string, match func(cp checkpoint.CrashCheckpoint) bool) checkpoint.CrashCheckpoint {
	t.Helper()

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			cp := readCrashCheckpoint(t, path)
			if match(cp) {
				return cp
			}
		}
		<-ticker.C
	}

	cp := readCrashCheckpoint(t, path)
	t.Fatalf("crash checkpoint %s did not reach %s within %v; latest timestamp=%s generation=%d", path, desc, timeout, cp.Timestamp.Format(time.RFC3339Nano), cp.Generation)
	return checkpoint.CrashCheckpoint{}
}

func waitForHistoryCaptureContains(t *testing.T, h *ServerHarness, pane, substr string, timeout time.Duration) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		out := h.runCmd("capture", "--history", pane)
		if strings.Contains(out, substr) {
			return out
		}
		<-ticker.C
	}

	out := h.runCmd("capture", "--history", pane)
	t.Fatalf("history capture for %s did not contain %q within %v, got:\n%s", pane, substr, timeout, out)
	return ""
}

func waitForCrashCheckpointPaneContains(t *testing.T, path, paneName string, prev checkpoint.CrashCheckpoint, timeout time.Duration, substrs ...string) checkpoint.CrashCheckpoint {
	t.Helper()

	return waitForCrashCheckpointMatch(t, path, timeout, fmt.Sprintf("fresh checkpoint containing %v for %s", substrs, paneName), func(cp checkpoint.CrashCheckpoint) bool {
		return (cp.Timestamp.After(prev.Timestamp) || cp.Generation > prev.Generation) && crashCheckpointPaneContains(cp, paneName, substrs...)
	})
}

func crashCheckpointPaneContains(cp checkpoint.CrashCheckpoint, paneName string, substrs ...string) bool {
	ps, ok := findCrashCheckpointPane(cp, paneName)
	if !ok {
		return false
	}
	text := strings.Join(ps.History, "\n") + "\n" + ps.Screen
	for _, substr := range substrs {
		if !strings.Contains(text, substr) {
			return false
		}
	}
	return true
}

func crashCheckpointPaneNamed(cp checkpoint.CrashCheckpoint, paneName string) bool {
	_, ok := findCrashCheckpointPane(cp, paneName)
	return ok
}

func crashCheckpointPaneWasIdle(cp checkpoint.CrashCheckpoint, paneName string) bool {
	ps, ok := findCrashCheckpointPane(cp, paneName)
	return ok && ps.WasIdle
}

func crashCheckpointWindowName(cp checkpoint.CrashCheckpoint) string {
	if len(cp.Layout.Windows) == 0 {
		return ""
	}
	return cp.Layout.Windows[0].Name
}

func crashCheckpointActivePaneName(cp checkpoint.CrashCheckpoint) string {
	for _, ps := range cp.PaneStates {
		if ps.ID == cp.Layout.ActivePaneID {
			return ps.Meta.Name
		}
	}
	return ""
}

func findCrashCheckpointPane(cp checkpoint.CrashCheckpoint, paneName string) (checkpoint.CrashPaneState, bool) {
	for _, ps := range cp.PaneStates {
		if ps.Meta.Name != paneName {
			continue
		}
		return ps, true
	}
	return checkpoint.CrashPaneState{}, false
}

// paneNames returns a comma-joined string of pane names from a capture (layout order).
func paneNames(c proto.CaptureJSON) string {
	var names []string
	for _, p := range c.Panes {
		names = append(names, p.Name)
	}
	return strings.Join(names, ",")
}

func readCrashCheckpoint(t *testing.T, path string) checkpoint.CrashCheckpoint {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading crash checkpoint %s: %v", path, err)
	}

	var cp checkpoint.CrashCheckpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		t.Fatalf("decoding crash checkpoint %s: %v", path, err)
	}
	return cp
}

func waitForFreshCrashCheckpoint(t *testing.T, path string, prev checkpoint.CrashCheckpoint, timeout time.Duration) checkpoint.CrashCheckpoint {
	t.Helper()
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			cp := readCrashCheckpoint(t, path)
			if cp.Timestamp.After(prev.Timestamp) || cp.Generation > prev.Generation {
				return cp
			}
		}
		<-ticker.C
	}

	t.Fatalf(
		"crash checkpoint %s was not refreshed within %v (prev timestamp=%s, generation=%d)",
		path,
		timeout,
		prev.Timestamp.Format(time.RFC3339Nano),
		prev.Generation,
	)
	return checkpoint.CrashCheckpoint{}
}

// startServerForSession starts a new server process for an existing session
// (used after crash to test recovery). Returns a harness connected to the
// recovered server.
func startServerForSession(t *testing.T, session, home string) *ServerHarness {
	t.Helper()

	// Create pipes for deterministic startup and clean-shutdown signals.
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating ready pipe: %v", err)
	}
	shutdownReadPipe, shutdownWritePipe, err := os.Pipe()
	if err != nil {
		readPipe.Close()
		writePipe.Close()
		t.Fatalf("creating shutdown pipe: %v", err)
	}

	cmd := exec.Command(amuxBin, "_server", session)
	cmd.ExtraFiles = []*os.File{writePipe, shutdownWritePipe}
	env := upsertEnv(os.Environ(), "HOME", home)
	env = append(env, "AMUX_READY_FD=3", "AMUX_SHUTDOWN_FD=4", "AMUX_NO_WATCH=1", "AMUX_EXIT_UNATTACHED=1")

	// Per-test cover dir
	var coverDir string
	if gocoverDir != "" {
		var b [4]byte
		rand.Read(b[:])
		coverDir = filepath.Join(gocoverDir, fmt.Sprintf("recover-%x", b))
		os.MkdirAll(coverDir, 0755)
		env = upsertEnv(env, "GOCOVERDIR", coverDir)
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
		shutdownReadPipe.Close()
		shutdownWritePipe.Close()
		t.Fatalf("starting recovered server: %v", err)
	}
	writePipe.Close()
	shutdownWritePipe.Close()
	logFile.Close()

	readPipe.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 64)
	n, err := readPipe.Read(buf)
	readPipe.Close()
	if err != nil || !strings.Contains(string(buf[:n]), "ready") {
		cmd.Process.Kill()
		shutdownReadPipe.Close()
		// Print server log for debugging
		logData, _ := os.ReadFile(logPath)
		t.Fatalf("recovered server ready signal not received: err=%v, buf=%q\nserver log:\n%s", err, string(buf[:n]), string(logData))
	}

	h := &ServerHarness{tb: t, session: session, cmd: cmd, home: home, coverDir: coverDir, shutdownPipe: shutdownReadPipe}
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
