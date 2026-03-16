package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/server"
)

// buildTestRenderer creates a ClientRenderer with two panes in a vertical split.
func buildTestRenderer(t *testing.T) *ClientRenderer {
	t.Helper()
	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(&proto.LayoutSnapshot{
		SessionName:  "test",
		ActivePaneID: 1,
		Width:        80,
		Height:       23, // 24 - 1 global bar
		Root: proto.CellSnapshot{
			X: 0, Y: 0, W: 80, H: 23,
			Dir: int(mux.SplitHorizontal),
			Children: []proto.CellSnapshot{
				{X: 0, Y: 0, W: 39, H: 23, IsLeaf: true, Dir: -1, PaneID: 1},
				{X: 40, Y: 0, W: 39, H: 23, IsLeaf: true, Dir: -1, PaneID: 2},
			},
		},
		Panes: []proto.PaneSnapshot{
			{ID: 1, Name: "pane-1", Host: "local", Color: "f5e0dc"},
			{ID: 2, Name: "pane-2", Host: "local", Color: "f2cdcd"},
		},
		Windows: []proto.WindowSnapshot{{
			ID: 1, Name: "window-1", Index: 1, ActivePaneID: 1,
			Root: proto.CellSnapshot{
				X: 0, Y: 0, W: 80, H: 23,
				Dir: int(mux.SplitHorizontal),
				Children: []proto.CellSnapshot{
					{X: 0, Y: 0, W: 39, H: 23, IsLeaf: true, Dir: -1, PaneID: 1},
					{X: 40, Y: 0, W: 39, H: 23, IsLeaf: true, Dir: -1, PaneID: 2},
				},
			},
			Panes: []proto.PaneSnapshot{
				{ID: 1, Name: "pane-1", Host: "local", Color: "f5e0dc"},
				{ID: 2, Name: "pane-2", Host: "local", Color: "f2cdcd"},
			},
		}},
		ActiveWindowID: 1,
	})
	// Write content into pane-1
	cr.HandlePaneOutput(1, []byte("hello from pane 1"))
	return cr
}

func TestClientRendererCapture(t *testing.T) {
	t.Parallel()
	cr := buildTestRenderer(t)

	// Plain text capture
	text := cr.Capture(true)
	if !strings.Contains(text, "pane-1") {
		t.Error("plain text capture should contain pane-1 status line")
	}

	// ANSI capture
	ansi := cr.Capture(false)
	if !strings.Contains(ansi, "\033[") {
		t.Error("ANSI capture should contain escape sequences")
	}
}

func TestClientRendererCaptureJSON(t *testing.T) {
	t.Parallel()
	cr := buildTestRenderer(t)

	out := cr.CaptureJSON(nil)
	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		t.Fatalf("JSON parse: %v\nraw: %s", err, out)
	}

	if capture.Session != "test" {
		t.Errorf("session: got %q, want %q", capture.Session, "test")
	}
	if capture.Window.ID != 1 {
		t.Errorf("window ID: got %d, want 1", capture.Window.ID)
	}
	if len(capture.Panes) != 2 {
		t.Fatalf("panes: got %d, want 2", len(capture.Panes))
	}

	// With agent status
	status := map[uint32]proto.PaneAgentStatus{
		1: {Idle: true, CurrentCommand: "bash", ChildPIDs: []int{}},
	}
	out2 := cr.CaptureJSON(status)
	var capture2 proto.CaptureJSON
	json.Unmarshal([]byte(out2), &capture2)

	for _, p := range capture2.Panes {
		if p.Name == "pane-1" && !p.Idle {
			t.Error("pane-1 should be idle with agent status applied")
		}
	}
}

func TestClientRendererCaptureColorMap(t *testing.T) {
	t.Parallel()
	cr := buildTestRenderer(t)
	cm := cr.CaptureColorMap()
	if cm == "" {
		t.Error("color map should not be empty")
	}
}

func TestClientRendererCapturePaneText(t *testing.T) {
	t.Parallel()
	cr := buildTestRenderer(t)

	text := cr.CapturePaneText(1, false)
	if !strings.Contains(text, "hello from pane 1") {
		t.Errorf("pane text should contain written content, got: %q", text)
	}

	ansi := cr.CapturePaneText(1, true)
	if ansi == "" {
		t.Error("ANSI pane text should not be empty")
	}

	empty := cr.CapturePaneText(999, false)
	if empty != "" {
		t.Error("nonexistent pane should return empty")
	}
}

func TestClientRendererCapturePaneJSON(t *testing.T) {
	t.Parallel()
	cr := buildTestRenderer(t)

	out := cr.CapturePaneJSON(1, nil)
	var cp proto.CapturePane
	if err := json.Unmarshal([]byte(out), &cp); err != nil {
		t.Fatalf("JSON parse: %v", err)
	}
	if cp.Name != "pane-1" {
		t.Errorf("name: got %q, want pane-1", cp.Name)
	}

	empty := cr.CapturePaneJSON(999, nil)
	if empty != "{}" {
		t.Errorf("nonexistent pane should return {}, got %q", empty)
	}
}

func TestClientRendererResolvePaneID(t *testing.T) {
	t.Parallel()
	cr := buildTestRenderer(t)

	if id := cr.ResolvePaneID("1"); id != 1 {
		t.Errorf("numeric: got %d, want 1", id)
	}
	if id := cr.ResolvePaneID("pane-2"); id != 2 {
		t.Errorf("name: got %d, want 2", id)
	}
	if id := cr.ResolvePaneID("pane-"); id == 0 {
		t.Error("prefix match should find a pane")
	}
	if id := cr.ResolvePaneID("nonexistent"); id != 0 {
		t.Errorf("nonexistent: got %d, want 0", id)
	}
}

func TestHandleCaptureRequest(t *testing.T) {
	t.Parallel()
	cr := buildTestRenderer(t)

	// Plain text
	resp := handleCaptureRequest(cr, []string{}, nil)
	if resp.Type != server.MsgTypeCaptureResponse {
		t.Errorf("type: got %d, want %d", resp.Type, server.MsgTypeCaptureResponse)
	}
	if resp.CmdOutput == "" {
		t.Error("plain capture should produce output")
	}

	// JSON
	resp = handleCaptureRequest(cr, []string{"--format", "json"}, nil)
	if resp.CmdErr != "" {
		t.Errorf("JSON capture error: %s", resp.CmdErr)
	}
	if !strings.Contains(resp.CmdOutput, `"session"`) {
		t.Error("JSON capture should contain session field")
	}

	// Single pane
	resp = handleCaptureRequest(cr, []string{"pane-1"}, nil)
	if !strings.Contains(resp.CmdOutput, "hello from pane 1") {
		t.Error("single pane capture should contain pane content")
	}

	// Color map
	resp = handleCaptureRequest(cr, []string{"--colors"}, nil)
	if resp.CmdErr != "" {
		t.Errorf("color map error: %s", resp.CmdErr)
	}

	// Mutual exclusivity
	resp = handleCaptureRequest(cr, []string{"--ansi", "--colors"}, nil)
	if resp.CmdErr == "" {
		t.Error("--ansi + --colors should error")
	}

	// Nonexistent pane
	resp = handleCaptureRequest(cr, []string{"nope"}, nil)
	if resp.CmdErr == "" {
		t.Error("nonexistent pane should error")
	}

	// --colors with pane ref
	resp = handleCaptureRequest(cr, []string{"--colors", "pane-1"}, nil)
	if resp.CmdErr == "" {
		t.Error("--colors with pane ref should error")
	}
}
