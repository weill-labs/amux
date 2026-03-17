package test

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/server"
)

// eventJSON is a minimal struct for parsing event stream output.
type eventJSON struct {
	Type       string `json:"type"`
	Timestamp  string `json:"ts"`
	Generation uint64 `json:"generation,omitempty"`
	PaneID     uint32 `json:"pane_id,omitempty"`
	PaneName   string `json:"pane_name,omitempty"`
	Host       string `json:"host,omitempty"`
	ActivePane string `json:"active_pane,omitempty"`
	TimedOut   bool   `json:"-"` // set by readEvent on timeout
}

// eventStream connects to the server's events command and returns a scanner
// that reads one JSON event per line, plus a close function.
func eventStream(t *testing.T, session string, args ...string) (*bufio.Scanner, func()) {
	t.Helper()
	sockPath := server.SocketPath(session)
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	cmdArgs := args
	if err := server.WriteMsg(conn, &server.Message{
		Type:    server.MsgTypeCommand,
		CmdName: "events",
		CmdArgs: cmdArgs,
	}); err != nil {
		conn.Close()
		t.Fatalf("write: %v", err)
	}

	// Read events by reading protocol messages and extracting CmdOutput
	pr, pw := net.Pipe()
	go func() {
		defer pw.Close()
		for {
			msg, err := server.ReadMsg(conn)
			if err != nil {
				return
			}
			if msg.CmdOutput != "" {
				pw.Write([]byte(msg.CmdOutput))
			}
		}
	}()

	scanner := bufio.NewScanner(pr)
	closer := func() {
		conn.Close()
		pr.Close()
	}
	return scanner, closer
}

// readEvent reads the next event from the scanner within timeout.
// Returns a zero eventJSON with TimedOut=true if the deadline expires.
func readEvent(t *testing.T, scanner *bufio.Scanner, timeout time.Duration) eventJSON {
	t.Helper()
	done := make(chan eventJSON, 1)
	go func() {
		if scanner.Scan() {
			var ev eventJSON
			if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
				return
			}
			done <- ev
		}
	}()

	select {
	case ev := <-done:
		return ev
	case <-time.After(timeout):
		return eventJSON{TimedOut: true}
	}
}

// mustReadEvent reads the next event, fataling on timeout.
func mustReadEvent(t *testing.T, scanner *bufio.Scanner, timeout time.Duration) eventJSON {
	t.Helper()
	ev := readEvent(t, scanner, timeout)
	if ev.TimedOut {
		t.Fatal("timeout reading event")
	}
	return ev
}

func TestEventsInitialSnapshot(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("flaky on CI: idle/busy events may not arrive within timeout (LAB-XXX)")
	}
	t.Parallel()
	h := newServerHarness(t)

	// Wait for the pane to be idle before subscribing to the event stream.
	// This ensures the idle state is established so the initial snapshot
	// includes it — avoids waiting for DefaultIdleTimeout on slow CI.
	h.waitIdle("pane-1")

	scanner, closer := eventStream(t, h.session)
	defer closer()

	// First event should be a layout snapshot with active_pane.
	ev := mustReadEvent(t, scanner, 5*time.Second)
	if ev.Type != "layout" {
		t.Fatalf("first event type: got %q, want layout", ev.Type)
	}
	if ev.ActivePane == "" {
		t.Error("layout event should have active_pane")
	}
	if ev.Timestamp == "" {
		t.Error("event should have a timestamp")
	}

	// Second event should be idle for pane-1 (we confirmed idle above).
	ev = mustReadEvent(t, scanner, 5*time.Second)
	if ev.Type != "idle" {
		t.Fatalf("second event type: got %q, want idle", ev.Type)
	}
	if ev.PaneName != "pane-1" {
		t.Errorf("pane name: got %q, want %q", ev.PaneName, "pane-1")
	}
}

func TestEventsLayoutOnSplit(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	scanner, closer := eventStream(t, h.session, "--filter", "layout")
	defer closer()

	// Drain initial layout snapshot
	mustReadEvent(t, scanner, 5*time.Second)

	// Split should emit a layout event
	h.doSplit()

	ev := mustReadEvent(t, scanner, 5*time.Second)
	if ev.Type != "layout" {
		t.Errorf("event type: got %q, want %q", ev.Type, "layout")
	}
	if ev.Generation == 0 {
		t.Error("layout event should have non-zero generation")
	}
}

func TestEventsFilterType(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Subscribe only to layout events
	scanner, closer := eventStream(t, h.session, "--filter", "layout")
	defer closer()

	// Drain initial layout snapshot
	mustReadEvent(t, scanner, 5*time.Second)

	// Generate output (should NOT produce an event since we're filtered to layout)
	h.sendKeys("pane-1", "echo hello", "Enter")

	// Split SHOULD produce a layout event
	h.doSplit()

	ev := mustReadEvent(t, scanner, 5*time.Second)
	if ev.Type != "layout" {
		t.Errorf("expected layout event, got %q", ev.Type)
	}
}

func TestEventsIdleBusyTransition(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Wait for pane to become idle first
	h.waitIdle("pane-1")

	// Subscribe to idle and busy events for pane-1
	scanner, closer := eventStream(t, h.session, "--filter", "idle,busy", "--pane", "pane-1")
	defer closer()

	// Drain initial snapshot (should be idle since we waited)
	ev := mustReadEvent(t, scanner, 5*time.Second)
	if ev.Type != "idle" {
		t.Errorf("initial state: got %q, want idle", ev.Type)
	}

	// Generate activity — should trigger busy transition
	h.sendKeys("pane-1", "echo activity", "Enter")

	ev = mustReadEvent(t, scanner, 5*time.Second)
	if ev.Type != "busy" {
		t.Errorf("after activity: got %q, want busy", ev.Type)
	}

	// Wait for idle timeout — should trigger idle transition
	ev = mustReadEvent(t, scanner, server.DefaultIdleTimeout+3*time.Second)
	if ev.Type != "idle" {
		t.Errorf("after quiet: got %q, want idle", ev.Type)
	}
}

func TestEventsFilterPane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV() // creates pane-2

	// Wait for both panes to become idle
	h.waitIdle("pane-1")
	h.waitIdle("pane-2")

	// Subscribe only to pane-1 events
	scanner, closer := eventStream(t, h.session, "--filter", "idle,busy", "--pane", "pane-1")
	defer closer()

	// Drain initial snapshot (idle for pane-1)
	ev := mustReadEvent(t, scanner, 5*time.Second)
	if ev.PaneName != "pane-1" {
		t.Errorf("initial event pane: got %q, want pane-1", ev.PaneName)
	}

	// Activity on pane-2 should NOT appear in pane-1's stream
	h.sendKeys("pane-2", "echo pane2activity", "Enter")

	// Activity on pane-1 SHOULD appear
	h.sendKeys("pane-1", "echo pane1activity", "Enter")

	ev = mustReadEvent(t, scanner, 5*time.Second)
	if ev.PaneName != "pane-1" {
		t.Errorf("filtered event should be for pane-1, got %q", ev.PaneName)
	}
}

// TestEventsCLI exercises `amux events` through the actual binary (covers
// main.go:runStreamingCommand and the CLI dispatch). The test reads stdout
// from the spawned process, verifies the initial snapshot arrives as valid
// NDJSON, then shuts down the server so the client exits normally and
// flushes coverage data.
func TestEventsCLI(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Spawn `amux events --filter layout` as a subprocess
	cmd := exec.Command(amuxBin, "-s", h.session, "events", "--filter", "layout")
	if h.coverDir != "" {
		cmd.Env = append(os.Environ(), "GOCOVERDIR="+h.coverDir)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	scanner := bufio.NewScanner(stdout)

	// Read initial layout snapshot from CLI stdout
	done := make(chan eventJSON, 1)
	go func() {
		if scanner.Scan() {
			var ev eventJSON
			json.Unmarshal(scanner.Bytes(), &ev)
			done <- ev
		}
	}()

	select {
	case ev := <-done:
		if ev.Type != "layout" {
			t.Errorf("first CLI event type: got %q, want layout", ev.Type)
		}
		if ev.ActivePane == "" {
			t.Error("CLI layout event should have active_pane")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout reading first event from CLI")
	}

	// Trigger a layout change and verify it arrives
	h.doSplit()

	done2 := make(chan eventJSON, 1)
	go func() {
		if scanner.Scan() {
			var ev eventJSON
			json.Unmarshal(scanner.Bytes(), &ev)
			done2 <- ev
		}
	}()

	select {
	case ev := <-done2:
		if ev.Type != "layout" {
			t.Errorf("second CLI event type: got %q, want layout", ev.Type)
		}
		if ev.Generation == 0 {
			t.Error("CLI layout event should have non-zero generation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout reading layout event from CLI after split")
	}

	// Shut down the server so the events client exits normally (via broken
	// pipe / EOF), allowing the -cover runtime to flush coverage data.
	// Kill sends SIGKILL which skips coverage flush.
	h.cmd.Process.Signal(os.Interrupt)
	waitDone := make(chan struct{})
	go func() {
		cmd.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		cmd.Wait()
	}
}

// TestEventsCLIServerNotRunning verifies that `amux events` exits with an
// error when no server is running (covers the error path in runStreamingCommand).
func TestEventsCLIServerNotRunning(t *testing.T) {
	t.Parallel()
	cmd := exec.Command(amuxBin, "-s", "nonexistent-session-xyz", "events")
	if gocoverDir != "" {
		cmd.Env = append(os.Environ(), "GOCOVERDIR="+gocoverDir)
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected error when server not running")
	}
	if exit, ok := err.(*exec.ExitError); ok {
		if exit.ExitCode() != 1 {
			t.Errorf("exit code: got %d, want 1", exit.ExitCode())
		}
	}
	if got := string(out); got == "" {
		t.Error("expected error message on stderr")
	}
}
