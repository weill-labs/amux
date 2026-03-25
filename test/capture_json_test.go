package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

// captureJSONPane is a test helper that captures JSON and returns the named pane.
func captureJSONPane(t *testing.T, h *ServerHarness, paneName string) proto.CapturePane {
	t.Helper()
	return h.jsonPane(h.captureJSON(), paneName)
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
	if capture.Height != 24 {
		t.Errorf("height: got %d, want 24 (full terminal height)", capture.Height)
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

func TestCaptureJSON_ClientUIState(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.runCmd("type-keys", "C-a", "q")
	h.runCmd("wait-ui", proto.UIEventDisplayPanesShown, "--timeout", "3s")

	out := h.runCmd("capture", "--format", "json")

	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		t.Fatalf("failed to parse JSON: %v\nraw output:\n%s", err, out)
	}
	if capture.UI == nil {
		t.Fatal("capture UI state should be present for live client capture")
	}
	if !capture.UI.DisplayPanes {
		t.Fatalf("expected display_panes=true, got %+v", capture.UI)
	}
	if !capture.UI.InputIdle {
		t.Fatalf("expected input_idle=true, got %+v", capture.UI)
	}
}

func TestCapturePaneJSON_CopyMode(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.sendKeys("C-a", "[")
	h.waitUI(proto.UIEventCopyModeShown, 3*time.Second)

	out := h.runCmd("capture", "--format", "json", "pane-1")

	var pane proto.CapturePane
	if err := json.Unmarshal([]byte(out), &pane); err != nil {
		t.Fatalf("failed to parse JSON: %v\nraw output:\n%s", err, out)
	}
	if !pane.CopyMode {
		t.Fatalf("expected pane copy_mode=true, got %+v", pane)
	}
}

func TestCaptureJSON_PreservesGraphemeClusters(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	line := "GRAPHEMES: Λ̊ 👍🏻 🤷‍♂️ 🇸🇪"
	doneMarker := "GRAPHEMES_DONE"
	scriptPath := filepath.Join(t.TempDir(), "graphemes.sh")
	script := "#!/bin/sh\nclear\nprintf '%s\\n' '" + line + "' '" + doneMarker + "'\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing grapheme script: %v", err)
	}

	h.sendKeys("pane-1", "sh "+scriptPath, "Enter")
	h.waitFor("pane-1", doneMarker)

	pane := captureJSONPane(t, h, "pane-1")
	found := false
	for _, got := range pane.Content {
		if strings.Contains(got, line) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("capture JSON should preserve grapheme clusters %q, got: %v", line, pane.Content)
	}
}

func TestCaptureJSON_AgentStatus_Busy(t *testing.T) {
	h := newPersistentHarnessWithCleanShutdown(t)

	h.startLongSleep("pane-1")

	pane1 := capturePaneJSONRetrying(t, "pane-1", h.runAttachedCmd)

	if pane1.Idle {
		t.Errorf("pane should not be idle (current_command=%q, child_pids=%v)", pane1.CurrentCommand, pane1.ChildPIDs)
	}
	if pane1.CurrentCommand == "" {
		t.Error("current_command should be non-empty")
	}
	if len(pane1.ChildPIDs) == 0 {
		t.Error("child_pids should be non-empty while command is running")
	}
	if pane1.IdleSince != "" {
		t.Errorf("idle_since should be empty when busy, got %q", pane1.IdleSince)
	}

	stopLongRunningCommand(t, h, "pane-1")
}

func TestCaptureJSON_AgentStatus_Idle(t *testing.T) {
	h := newPersistentHarnessWithCleanShutdown(t)

	// Shell at prompt — wait for idle timer. No retry loop needed: capture
	// JSON uses the server's cached idleState (same source as waitIdle).
	h.sendKeys("pane-1", "echo READY", "Enter")
	h.waitFor("pane-1", "READY")
	h.waitIdle("pane-1")

	pane := capturePaneJSONRetrying(t, "pane-1", h.runAttachedCmd)

	if !pane.Idle {
		t.Errorf("pane should be idle (current_command=%q, child_pids=%v)", pane.CurrentCommand, pane.ChildPIDs)
	}
	if pane.IdleSince == "" {
		t.Error("idle_since should be set when pane is idle")
	}
	if _, err := time.Parse(time.RFC3339, pane.IdleSince); err != nil {
		t.Errorf("idle_since should be RFC3339, got %q: %v", pane.IdleSince, err)
	}
	if pane.CurrentCommand == "" {
		t.Error("current_command should report the shell even when idle")
	}
}

func TestCaptureJSON_AgentStatus_SinglePane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.startLongSleep("pane-1")

	// Single-pane capture should also include agent status
	out := h.runCmd("capture", "--format", "json", "pane-1")

	var pane proto.CapturePane
	if err := json.Unmarshal([]byte(out), &pane); err != nil {
		t.Fatalf("failed to parse JSON: %v\nraw output:\n%s", err, out)
	}

	if pane.Idle {
		t.Errorf("pane should not be idle (current_command=%q, child_pids=%v)", pane.CurrentCommand, pane.ChildPIDs)
	}
	if pane.CurrentCommand == "" {
		t.Error("current_command should be non-empty while command is running")
	}
	if len(pane.ChildPIDs) == 0 {
		t.Error("child_pids should be non-empty")
	}
}

func TestCaptureJSON_AgentStatus_Transition(t *testing.T) {
	h := newPersistentHarnessWithCleanShutdown(t)

	// Start idle — confirm initial state
	h.sendKeys("pane-1", "echo INIT", "Enter")
	h.waitFor("pane-1", "INIT")
	h.waitIdle("pane-1")

	pane := capturePaneJSONRetrying(t, "pane-1", h.runAttachedCmd)
	if !pane.Idle {
		t.Fatal("pane should start idle")
	}
	if pane.IdleSince == "" {
		t.Fatal("idle_since should be set initially")
	}

	// Transition to busy
	h.startLongSleep("pane-1")

	pane = capturePaneJSONRetrying(t, "pane-1", h.runAttachedCmd)
	if pane.Idle {
		t.Error("pane should be busy after running sleep")
	}
	if pane.IdleSince != "" {
		t.Errorf("idle_since should be empty when busy, got %q", pane.IdleSince)
	}

	stopLongRunningCommand(t, h, "pane-1")
}

func TestCaptureJSON_AgentStatus_ChildPIDsArray(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Even when idle, child_pids should be a JSON array (not null).
	h.sendKeys("pane-1", "echo ARRAY_TEST", "Enter")
	h.waitFor("pane-1", "ARRAY_TEST")
	h.waitIdle("pane-1")

	out := h.runCmd("capture", "--format", "json", "pane-1")
	if !strings.Contains(out, `"child_pids": []`) {
		t.Errorf("idle pane should have child_pids as empty array, got:\n%s", out)
	}
}

func TestCaptureJSON_AgentStatus_MultiPane(t *testing.T) {
	h := newPersistentHarnessWithCleanShutdown(t)

	h.splitV() // creates pane-2

	// Make pane-1 busy, leave pane-2 idle
	h.startLongSleep("pane-1")
	h.sendKeys("pane-2", "echo IDLE_CHECK", "Enter")
	h.waitFor("pane-2", "IDLE_CHECK")
	h.waitIdle("pane-2")

	capture := captureJSONRetrying(t, h.runAttachedCmd)

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

	stopLongRunningCommand(t, h, "pane-1")
}
