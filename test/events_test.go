package test

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/server"
)

func parseClientIDs(listing string) []string {
	var ids []string
	for _, line := range strings.Split(listing, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if strings.HasPrefix(fields[0], "client-") {
			ids = append(ids, fields[0])
		}
	}
	return ids
}

func TestEventsInitialSnapshot(t *testing.T) {
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

	// Drain events until we see idle for pane-1. The exact event order
	// between layout and idle depends on shell timing, so accept
	// intervening events gracefully.
	for {
		ev = readEvent(t, scanner, 5*time.Second)
		if ev.TimedOut {
			t.Fatal("timeout waiting for idle event for pane-1")
		}
		if ev.Type == "idle" && ev.PaneName == "pane-1" {
			break
		}
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

func TestEventsClientUISnapshotAndUpdates(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	scanner, closer := eventStream(t, h.session, "--filter", proto.UIEventDisplayPanesHidden+","+proto.UIEventDisplayPanesShown, "--client", "client-1")
	defer closer()

	ev := mustReadEvent(t, scanner, 5*time.Second)
	if ev.Type != proto.UIEventDisplayPanesHidden {
		t.Fatalf("initial UI state: got %q, want %q", ev.Type, proto.UIEventDisplayPanesHidden)
	}
	if ev.ClientID != "client-1" {
		t.Fatalf("client_id: got %q, want client-1", ev.ClientID)
	}

	h.client.sendUIEvent(proto.UIEventDisplayPanesShown)
	ev = mustReadEvent(t, scanner, 5*time.Second)
	if ev.Type != proto.UIEventDisplayPanesShown {
		t.Fatalf("updated UI state: got %q, want %q", ev.Type, proto.UIEventDisplayPanesShown)
	}
	if ev.ClientID != "client-1" {
		t.Fatalf("client_id: got %q, want client-1", ev.ClientID)
	}
}

func TestEventsFilterClient(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	second, err := newHeadlessClient(server.SocketPath(h.session), h.session, 80, 24)
	if err != nil {
		t.Fatalf("attaching second client: %v", err)
	}
	defer second.close()

	clients := parseClientIDs(h.runCmd("list-clients"))
	if len(clients) != 2 {
		t.Fatalf("attached clients = %v, want 2", clients)
	}
	secondID := clients[1]

	scanner, closer := eventStream(t, h.session, "--filter", proto.UIEventDisplayPanesHidden, "--client", secondID)
	defer closer()

	ev := mustReadEvent(t, scanner, 5*time.Second)
	if ev.Type != proto.UIEventDisplayPanesHidden || ev.ClientID != secondID {
		t.Fatalf("initial event = %+v, want hidden for %s", ev, secondID)
	}

	h.client.sendUIEvent(proto.UIEventDisplayPanesShown)
	if ev := readEvent(t, scanner, 200*time.Millisecond); !ev.TimedOut {
		t.Fatalf("client-1 event should not match %s filter, got %+v", secondID, ev)
	}
}

func TestWaitUIImmediateHidden(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	out := h.runCmd("wait-ui", proto.UIEventDisplayPanesHidden, "--timeout", "1s")
	if strings.Contains(out, "timeout") {
		t.Fatalf("wait-ui hidden should return immediately, got: %s", out)
	}
	if !strings.Contains(out, proto.UIEventDisplayPanesHidden) {
		t.Fatalf("wait-ui hidden output = %q", out)
	}
}

func TestWaitUICopyModeTransitions(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	h.sendKeys("C-a", "[")
	h.waitUI(proto.UIEventCopyModeShown, 3*time.Second)
	h.sendKeys("q")
	h.waitUI(proto.UIEventCopyModeHidden, 3*time.Second)
}

func TestWaitUIInputIdleAfterTypeKeys(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	out := h.runCmd("wait-ui", proto.UIEventInputIdle, "--timeout", "1s")
	if !strings.Contains(out, proto.UIEventInputIdle) {
		t.Fatalf("wait-ui input-idle output = %q", out)
	}

	h.runCmd("type-keys", "e", "c", "h", "o", " ", "INPUT_IDLE_OK", "Enter")
	h.waitUI(proto.UIEventInputIdle, 3*time.Second)
	if !h.waitFor("INPUT_IDLE_OK", 3*time.Second) {
		t.Fatalf("expected INPUT_IDLE_OK after type-keys\nScreen:\n%s", h.captureOuter())
	}
}

func TestWaitHookOnIdle(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	tmp := t.TempDir()
	marker := filepath.Join(tmp, "hook-wait")

	after := strings.TrimSpace(h.runCmd("hook-gen"))
	h.runCmd("set-hook", "on-idle", "touch "+marker)
	h.sendKeys("pane-1", "echo HOOKWAIT", "Enter")
	h.waitFor("pane-1", "HOOKWAIT")

	out := h.runCmd("wait-hook", "on-idle", "--pane", "pane-1", "--after", after, "--timeout", "5s")
	if strings.Contains(out, "timeout") {
		t.Fatalf("wait-hook timed out: %s", out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker missing after wait-hook: %v", err)
	}
}

func TestWaitUIRequiresClientWhenAmbiguous(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	second, err := newHeadlessClient(server.SocketPath(h.session), h.session, 80, 24)
	if err != nil {
		t.Fatalf("attaching second client: %v", err)
	}
	defer second.close()

	clients := parseClientIDs(h.runCmd("list-clients"))
	out := h.runCmd("wait-ui", proto.UIEventDisplayPanesHidden, "--timeout", "1s")
	if !strings.Contains(out, "multiple clients attached") {
		t.Fatalf("expected ambiguous wait-ui error, got: %s", out)
	}
	for _, id := range clients {
		if !strings.Contains(out, id) {
			t.Fatalf("expected listed client ID %s, got: %s", id, out)
		}
	}
}

func TestListClientsShowsDisplayPanesState(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.client.sendUIEvent(proto.UIEventDisplayPanesShown)
	h.client.sendUIEvent(proto.UIEventChooseWindowShown)

	out := h.runCmd("list-clients")
	if !strings.Contains(out, "CLIENT") || !strings.Contains(out, "DISPLAY_PANES") || !strings.Contains(out, "CHOOSER") {
		t.Fatalf("unexpected list-clients header: %s", out)
	}
	if !strings.Contains(out, "client-1") || !strings.Contains(out, "shown") || !strings.Contains(out, "window") {
		t.Fatalf("list-clients should report shown state, got: %s", out)
	}
}

func TestEventsChooserUISnapshotAndUpdates(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	scanner, closer := eventStream(t, h.session, "--filter", proto.UIEventChooseTreeHidden+","+proto.UIEventChooseTreeShown, "--client", "client-1")
	defer closer()

	ev := mustReadEvent(t, scanner, 5*time.Second)
	if ev.Type != proto.UIEventChooseTreeHidden {
		t.Fatalf("initial chooser state: got %q, want %q", ev.Type, proto.UIEventChooseTreeHidden)
	}

	h.client.sendUIEvent(proto.UIEventChooseTreeShown)
	ev = mustReadEvent(t, scanner, 5*time.Second)
	if ev.Type != proto.UIEventChooseTreeShown {
		t.Fatalf("updated chooser state: got %q, want %q", ev.Type, proto.UIEventChooseTreeShown)
	}
}

func TestWaitUIImmediateChooseWindowHidden(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	out := h.runCmd("wait-ui", proto.UIEventChooseWindowHidden, "--timeout", "1s")
	if strings.Contains(out, "timeout") {
		t.Fatalf("wait-ui choose-window-hidden should return immediately, got: %s", out)
	}
	if !strings.Contains(out, proto.UIEventChooseWindowHidden) {
		t.Fatalf("wait-ui choose-window-hidden output = %q", out)
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
