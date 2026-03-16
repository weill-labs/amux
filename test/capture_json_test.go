package test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

// captureJSONPane is a test helper that captures JSON and returns the named pane.
func captureJSONPane(t *testing.T, h *ServerHarness, paneName string) proto.CapturePane {
	t.Helper()
	out := h.runCmd("capture", "--format", "json")
	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		t.Fatalf("failed to parse JSON: %v\nraw output:\n%s", err, out)
	}
	for _, p := range capture.Panes {
		if p.Name == paneName {
			return p
		}
	}
	t.Fatalf("pane %q not found in JSON output", paneName)
	return proto.CapturePane{}
}

func TestCaptureJSON_FullScreen(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.sendKeys("pane-1", "echo JSONTEST", "Enter")
	h.waitFor("pane-1", "JSONTEST")

	h.splitV()

	out := h.runCmd("capture", "--format", "json")

	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		t.Fatalf("failed to parse JSON: %v\nraw output:\n%s", err, out)
	}

	if capture.Session == "" {
		t.Error("session should be non-empty")
	}
	if capture.Window.ID == 0 {
		t.Error("window ID should be non-zero")
	}
	if capture.Width != 80 {
		t.Errorf("width: got %d, want 80", capture.Width)
	}
	if len(capture.Panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(capture.Panes))
	}

	// Check pane-1 has content
	var pane1 *proto.CapturePane
	for i := range capture.Panes {
		if capture.Panes[i].Name == "pane-1" {
			pane1 = &capture.Panes[i]
			break
		}
	}
	if pane1 == nil {
		t.Fatal("pane-1 not found in JSON output")
	}

	found := false
	for _, line := range pane1.Content {
		if strings.Contains(line, "JSONTEST") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("pane-1 content should contain JSONTEST, got: %v", pane1.Content)
	}

	// Check positions are present
	for _, p := range capture.Panes {
		if p.Position == nil {
			t.Errorf("pane %s: position should be present", p.Name)
		}
	}
}

func TestCaptureJSON_SinglePane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.sendKeys("pane-1", "echo SINGLEPANE", "Enter")
	h.waitFor("pane-1", "SINGLEPANE")

	out := h.runCmd("capture", "--format", "json", "pane-1")

	var pane proto.CapturePane
	if err := json.Unmarshal([]byte(out), &pane); err != nil {
		t.Fatalf("failed to parse JSON: %v\nraw output:\n%s", err, out)
	}

	if pane.Name != "pane-1" {
		t.Errorf("name: got %q, want %q", pane.Name, "pane-1")
	}
	if !pane.Active {
		t.Error("pane-1 should be active")
	}
	if pane.Position != nil {
		t.Error("single-pane capture should not include position")
	}

	found := false
	for _, line := range pane.Content {
		if strings.Contains(line, "SINGLEPANE") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("content should contain SINGLEPANE, got: %v", pane.Content)
	}

	// Content should be padded to pane height.
	// Harness seeds with Rows=24. Layout height = 24-1 (global bar) = 23.
	// Pane content height = 23-1 (status line) = 22.
	if len(pane.Content) != 22 {
		t.Errorf("content lines: got %d, want 22", len(pane.Content))
	}
}

func TestCaptureJSON_Minimized(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH() // top/bottom split required for minimize
	h.runCmd("minimize", "pane-1")

	out := h.runCmd("capture", "--format", "json")

	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		t.Fatalf("failed to parse JSON: %v\nraw output:\n%s", err, out)
	}

	for _, p := range capture.Panes {
		if p.Name == "pane-1" {
			if !p.Minimized {
				t.Error("pane-1 should be minimized")
			}
			return
		}
	}
	t.Error("pane-1 not found in JSON output")
}

func TestCaptureJSON_Zoomed(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	h.runCmd("zoom", "pane-1")

	out := h.runCmd("capture", "--format", "json")

	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		t.Fatalf("failed to parse JSON: %v\nraw output:\n%s", err, out)
	}

	for _, p := range capture.Panes {
		if p.Name == "pane-1" {
			if !p.Zoomed {
				t.Error("pane-1 should be zoomed")
			}
			// Zoomed pane should fill the window
			if p.Position != nil && p.Position.Width != 80 {
				t.Errorf("zoomed pane width: got %d, want 80", p.Position.Width)
			}
			return
		}
	}
	t.Error("pane-1 not found in JSON output")
}

func TestCaptureJSON_MutualExclusivity(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("capture", "--format", "json", "--ansi")
	if !strings.Contains(out, "mutually exclusive") {
		t.Errorf("expected mutual exclusivity error, got: %s", out)
	}
}

func TestCaptureJSON_RoundTrip(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.sendKeys("pane-1", "echo ROUNDTRIP", "Enter")
	h.waitFor("pane-1", "ROUNDTRIP")

	// Capture as both plain text and JSON
	plain := h.runCmd("capture", "pane-1")
	jsonOut := h.runCmd("capture", "--format", "json", "pane-1")

	var pane proto.CapturePane
	if err := json.Unmarshal([]byte(jsonOut), &pane); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Verify every non-empty line from plain text appears in JSON content.
	for _, line := range strings.Split(strings.TrimSpace(plain), "\n") {
		trimmed := strings.TrimRight(line, " ")
		if trimmed == "" {
			continue
		}
		found := false
		for _, jline := range pane.Content {
			if strings.Contains(jline, trimmed) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("plain text line %q not found in JSON content", trimmed)
		}
	}
}

func TestCaptureJSON_AgentStatus_Busy(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.startLongSleep("pane-1")

	// Poll until AgentStatus agrees with waitBusy — separate pgrep calls
	// can briefly disagree on slow CI.
	var pane1 proto.CapturePane
	deadline := time.Now().Add(3 * time.Second)
	for {
		pane1 = captureJSONPane(t, h, "pane-1")
		if !pane1.Idle && pane1.CurrentCommand != "" && len(pane1.ChildPIDs) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane should be busy (idle=%v, current_command=%q, child_pids=%v)",
				pane1.Idle, pane1.CurrentCommand, pane1.ChildPIDs)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pane1.IdleSince != "" {
		t.Errorf("idle_since should be empty when busy, got %q", pane1.IdleSince)
	}
}

func TestCaptureJSON_AgentStatus_Idle(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Shell at prompt — send a harmless command and wait for its output
	// to confirm the shell is initialized and idle. Poll the JSON capture
	// until AgentStatus agrees, since waitIdle and forwardCapture use
	// separate pgrep calls that can briefly disagree on slow CI.
	h.sendKeys("pane-1", "echo READY", "Enter")
	h.waitFor("pane-1", "READY")

	// Retry briefly — under parallel load, pgrep may see transient shell
	// children (job-control self-fork) that settle within a few hundred ms.
	var pane proto.CapturePane
	deadline := time.Now().Add(3 * time.Second)
	for {
		out := h.runCmd("capture", "--format", "json", "pane-1")
		if err := json.Unmarshal([]byte(out), &pane); err != nil {
			t.Fatalf("failed to parse JSON: %v\nraw output:\n%s", err, out)
		}
		if pane.Idle && pane.IdleSince != "" && pane.CurrentCommand != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane should be idle with all fields populated (idle=%v, idle_since=%q, current_command=%q)",
				pane.Idle, pane.IdleSince, pane.CurrentCommand)
		}
		time.Sleep(100 * time.Millisecond)
	}

	if _, err := time.Parse(time.RFC3339, pane.IdleSince); err != nil {
		t.Errorf("idle_since should be RFC3339, got %q: %v", pane.IdleSince, err)
	}
}

func TestCaptureJSON_AgentStatus_SinglePane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.startLongSleep("pane-1")

	// Single-pane capture should also include agent status. Poll until
	// AgentStatus agrees — separate pgrep calls can briefly disagree.
	var pane proto.CapturePane
	deadline := time.Now().Add(3 * time.Second)
	for {
		out := h.runCmd("capture", "--format", "json", "pane-1")
		if err := json.Unmarshal([]byte(out), &pane); err != nil {
			t.Fatalf("failed to parse JSON: %v\nraw output:\n%s", err, out)
		}
		if !pane.Idle && pane.CurrentCommand != "" && len(pane.ChildPIDs) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane should be busy (idle=%v, current_command=%q, child_pids=%v)",
				pane.Idle, pane.CurrentCommand, pane.ChildPIDs)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestCaptureJSON_AgentStatus_Transition(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Start idle — confirm initial state. Poll the JSON capture until
	// AgentStatus agrees with waitIdle, since they use separate pgrep calls
	// that can briefly disagree on slow CI (shell PROMPT_COMMAND, etc.).
	h.sendKeys("pane-1", "echo INIT", "Enter")
	h.waitFor("pane-1", "INIT")
	h.waitIdle("pane-1")

	var pane proto.CapturePane
	deadline := time.Now().Add(3 * time.Second)
	for {
		pane = captureJSONPane(t, h, "pane-1")
		if pane.Idle && pane.IdleSince != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane should start idle (idle=%v, idle_since=%q)", pane.Idle, pane.IdleSince)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Transition to busy
	h.startLongSleep("pane-1")

	pane = captureJSONPane(t, h, "pane-1")
	if pane.Idle {
		t.Error("pane should be busy after running sleep")
	}
	if pane.IdleSince != "" {
		t.Errorf("idle_since should be empty when busy, got %q", pane.IdleSince)
	}
}

func TestCaptureJSON_AgentStatus_ChildPIDsArray(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Even when idle, child_pids should be a JSON array (not null).
	// Retry briefly — bash's job-control self-fork can create a transient
	// child process that settles within ~100ms.
	h.sendKeys("pane-1", "echo ARRAY_TEST", "Enter")
	h.waitFor("pane-1", "ARRAY_TEST")

	deadline := time.Now().Add(3 * time.Second)
	var out string
	for time.Now().Before(deadline) {
		out = h.runCmd("capture", "--format", "json", "pane-1")
		if strings.Contains(out, `"child_pids": []`) {
			return // pass
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("idle pane should have child_pids as empty array, got:\n%s", out)
}

func TestCaptureJSON_AgentStatus_MultiPane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV() // creates pane-2

	// Make pane-1 busy, leave pane-2 idle
	h.startLongSleep("pane-1")
	h.sendKeys("pane-2", "echo IDLE_CHECK", "Enter")
	h.waitFor("pane-2", "IDLE_CHECK")
	h.waitIdle("pane-2")

	out := h.runCmd("capture", "--format", "json")
	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		t.Fatalf("failed to parse JSON: %v\nraw output:\n%s", err, out)
	}

	for _, p := range capture.Panes {
		switch p.Name {
		case "pane-1":
			if p.Idle {
				t.Error("pane-1 should be busy")
			}
		case "pane-2":
			if !p.Idle {
				t.Error("pane-2 should be idle")
			}
		}
	}
}

