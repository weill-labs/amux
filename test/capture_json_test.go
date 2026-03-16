package test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

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
