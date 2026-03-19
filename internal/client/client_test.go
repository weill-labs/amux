package client

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

// twoPane80x23 returns a layout snapshot with two panes in a vertical split
// at 80 columns by 23 rows (the standard 80x24 terminal minus the global bar).
func twoPane80x23() *proto.LayoutSnapshot {
	root := proto.CellSnapshot{
		X: 0, Y: 0, W: 80, H: 23,
		Dir: int(mux.SplitVertical),
		Children: []proto.CellSnapshot{
			{X: 0, Y: 0, W: 39, H: 23, IsLeaf: true, Dir: -1, PaneID: 1},
			{X: 40, Y: 0, W: 39, H: 23, IsLeaf: true, Dir: -1, PaneID: 2},
		},
	}
	panes := []proto.PaneSnapshot{
		{ID: 1, Name: "pane-1", Host: "local", Color: "f5e0dc"},
		{ID: 2, Name: "pane-2", Host: "local", Color: "f2cdcd"},
	}
	return &proto.LayoutSnapshot{
		SessionName:  "test",
		ActivePaneID: 1,
		Width:        80,
		Height:       23,
		Root:         root,
		Panes:        panes,
		Windows: []proto.WindowSnapshot{{
			ID: 1, Name: "window-1", Index: 1, ActivePaneID: 1,
			Root:  root,
			Panes: panes,
		}},
		ActiveWindowID: 1,
	}
}

// buildTestRenderer creates a ClientRenderer with two panes in a vertical split.
func buildTestRenderer(t *testing.T) *ClientRenderer {
	t.Helper()
	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(twoPane80x23())
	cr.HandlePaneOutput(1, []byte("hello from pane 1"))
	return cr
}

func twoPane80x23Zoomed(paneID uint32) *proto.LayoutSnapshot {
	snap := twoPane80x23()
	snap.ZoomedPaneID = paneID
	if len(snap.Windows) > 0 {
		snap.Windows[0].ZoomedPaneID = paneID
	}
	return snap
}

func buildManyPaneRenderer(t *testing.T, n int) *ClientRenderer {
	t.Helper()
	cr := NewClientRenderer(200, 24)
	children := make([]proto.CellSnapshot, 0, n)
	panes := make([]proto.PaneSnapshot, 0, n)
	x := 0
	for i := 1; i <= n; i++ {
		w := 4
		if i == n {
			w = 200 - x
		}
		children = append(children, proto.CellSnapshot{
			X: x, Y: 0, W: w, H: 23, IsLeaf: true, Dir: -1, PaneID: uint32(i),
		})
		panes = append(panes, proto.PaneSnapshot{
			ID: uint32(i), Name: fmt.Sprintf("pane-%d", i), Host: "local", Color: "f5e0dc",
		})
		x += w + 1
	}
	cr.HandleLayout(&proto.LayoutSnapshot{
		SessionName:  "test",
		ActivePaneID: 1,
		Width:        200,
		Height:       23,
		Root: proto.CellSnapshot{
			X: 0, Y: 0, W: 200, H: 23,
			Dir:      int(mux.SplitVertical),
			Children: children,
		},
		Panes: panes,
	})
	return cr
}

func multiWindow80x23() *proto.LayoutSnapshot {
	window1Root := proto.CellSnapshot{
		X: 0, Y: 0, W: 80, H: 23,
		Dir: int(mux.SplitVertical),
		Children: []proto.CellSnapshot{
			{X: 0, Y: 0, W: 39, H: 23, IsLeaf: true, Dir: -1, PaneID: 1},
			{X: 40, Y: 0, W: 39, H: 23, IsLeaf: true, Dir: -1, PaneID: 2},
		},
	}
	window2Root := proto.CellSnapshot{
		X: 0, Y: 0, W: 80, H: 23,
		IsLeaf: true, Dir: -1, PaneID: 3,
	}
	return &proto.LayoutSnapshot{
		SessionName:  "test",
		ActivePaneID: 1,
		Width:        80,
		Height:       23,
		Root:         window1Root,
		Windows: []proto.WindowSnapshot{
			{
				ID: 1, Name: "editor", Index: 1, ActivePaneID: 1,
				Root: window1Root,
				Panes: []proto.PaneSnapshot{
					{ID: 1, Name: "pane-1", Host: "local", Task: "server", Color: "f5e0dc"},
					{ID: 2, Name: "pane-2", Host: "gpu-box", Task: "train", Color: "f2cdcd"},
				},
			},
			{
				ID: 2, Name: "logs", Index: 2, ActivePaneID: 3,
				Root: window2Root,
				Panes: []proto.PaneSnapshot{
					{ID: 3, Name: "pane-3", Host: "local", Task: "tail", Color: "cba6f7"},
				},
			},
		},
		ActiveWindowID: 1,
	}
}

func buildMultiWindowRenderer(t *testing.T) *ClientRenderer {
	t.Helper()
	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(multiWindow80x23())
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

	cr.renderer.mu.Lock()
	info := cr.renderer.paneInfo[2]
	info.Host = "test-remote"
	info.ConnStatus = "connected"
	cr.renderer.paneInfo[2] = info
	cr.renderer.mu.Unlock()

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
	foundRemote := false
	for _, p := range capture.Panes {
		if p.Name == "pane-2" {
			foundRemote = true
			if p.ConnStatus != "connected" {
				t.Fatalf("pane-2 conn_status = %q, want connected", p.ConnStatus)
			}
		}
	}
	if !foundRemote {
		t.Fatal("pane-2 missing from capture")
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

func TestClientRendererZoomedPaneSurvivesMetadataOnlyLayout(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(twoPane80x23Zoomed(2))

	const wideLine = "012345678901234567890123456789012345678901234567890123456789"
	cr.HandlePaneOutput(2, []byte("\033[2J\033[H"+wideLine))

	emu, ok := cr.Emulator(2)
	if !ok {
		t.Fatal("pane-2 emulator missing")
	}
	if w, h := emu.Size(); w != 80 || h != 22 {
		t.Fatalf("zoomed pane-2 size after initial layout = %dx%d, want 80x22", w, h)
	}

	idleSnap := twoPane80x23Zoomed(2)
	idleSnap.Panes[1].Idle = true
	idleSnap.Windows[0].Panes[1].Idle = true
	cr.HandleLayout(idleSnap)

	emu, ok = cr.Emulator(2)
	if !ok {
		t.Fatal("pane-2 emulator missing after idle layout")
	}
	if w, h := emu.Size(); w != 80 || h != 22 {
		t.Fatalf("zoomed pane-2 size after idle layout = %dx%d, want 80x22", w, h)
	}

	lines := strings.Split(cr.CapturePaneText(2, false), "\n")
	if len(lines) == 0 || lines[0] != wideLine {
		t.Fatalf("pane-2 first line after idle layout = %q, want %q", lines[0], wideLine)
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

func TestCaptureDisplay(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)

	// Before any layout, CaptureDisplay returns empty.
	if got := cr.CaptureDisplay(); got != "" {
		t.Errorf("before layout: CaptureDisplay = %q, want empty", got)
	}

	cr = buildTestRenderer(t)

	// After layout but before diff render, prevGrid is nil (HandleLayout
	// calls Resize which clears it). Force a diff render.
	cr.RenderDiff()

	got := cr.CaptureDisplay()
	if got == "" {
		t.Fatal("after RenderDiff: CaptureDisplay is empty")
	}
	if !strings.Contains(got, "pane-1") {
		t.Error("CaptureDisplay should contain pane status line")
	}
	if !strings.Contains(got, "hello from pane 1") {
		t.Error("CaptureDisplay should contain pane content")
	}
}

func TestDisplayPanesOverlayDisplayOnly(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	if !cr.ShowDisplayPanes() {
		t.Fatal("ShowDisplayPanes should succeed for two panes")
	}
	cr.RenderDiff()

	display := cr.CaptureDisplay()
	if !strings.Contains(display, "[1]") || !strings.Contains(display, "[2]") {
		t.Fatalf("display capture should include overlay labels, got:\n%s", display)
	}

	plain := cr.Capture(true)
	if strings.Contains(plain, "[1]") || strings.Contains(plain, "[2]") {
		t.Fatalf("plain capture should not include overlay labels, got:\n%s", plain)
	}

	resp := cr.HandleCaptureRequest([]string{"--display"}, nil)
	if !strings.Contains(resp.CmdOutput, "[1]") {
		t.Fatalf("--display should include overlay labels, got:\n%s", resp.CmdOutput)
	}

	resp = cr.HandleCaptureRequest([]string{}, nil)
	if strings.Contains(resp.CmdOutput, "[1]") || strings.Contains(resp.CmdOutput, "[2]") {
		t.Fatalf("plain capture request should not include overlay labels, got:\n%s", resp.CmdOutput)
	}
}

func TestDisplayPanesLabelResolution(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	if !cr.ShowDisplayPanes() {
		t.Fatal("ShowDisplayPanes should succeed")
	}

	if paneID, ok := cr.ResolveDisplayPaneKey('1'); !ok || paneID != 1 {
		t.Fatalf("label 1 should resolve to pane-1, got pane=%d ok=%v", paneID, ok)
	}
	if paneID, ok := cr.ResolveDisplayPaneKey('2'); !ok || paneID != 2 {
		t.Fatalf("label 2 should resolve to pane-2, got pane=%d ok=%v", paneID, ok)
	}
}

func TestShowDisplayPanesTooManyPanes(t *testing.T) {
	t.Parallel()

	cr := buildManyPaneRenderer(t, len(displayPaneLabelAlphabet)+1)
	if cr.ShowDisplayPanes() {
		t.Fatal("ShowDisplayPanes should fail when pane count exceeds label capacity")
	}
}

func TestShowDisplayPanesZoomedOnlyLabelsVisiblePane(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.HandleLayout(&proto.LayoutSnapshot{
		SessionName:  "test",
		ActivePaneID: 1,
		ZoomedPaneID: 2,
		Width:        80,
		Height:       23,
		Root: proto.CellSnapshot{
			X: 0, Y: 0, W: 80, H: 23,
			Dir: int(mux.SplitVertical),
			Children: []proto.CellSnapshot{
				{X: 0, Y: 0, W: 39, H: 23, IsLeaf: true, Dir: -1, PaneID: 1},
				{X: 40, Y: 0, W: 39, H: 23, IsLeaf: true, Dir: -1, PaneID: 2},
			},
		},
		Panes: []proto.PaneSnapshot{
			{ID: 1, Name: "pane-1", Host: "local", Color: "f5e0dc"},
			{ID: 2, Name: "pane-2", Host: "local", Color: "f2cdcd"},
		},
	})

	if !cr.ShowDisplayPanes() {
		t.Fatal("ShowDisplayPanes should succeed in zoom mode")
	}
	if paneID, ok := cr.ResolveDisplayPaneKey('1'); !ok || paneID != 2 {
		t.Fatalf("visible zoomed pane should be relabeled as 1, got pane=%d ok=%v", paneID, ok)
	}
	if paneID, ok := cr.ResolveDisplayPaneKey('2'); ok || paneID != 0 {
		t.Fatalf("hidden pane should not get a visible label in zoom mode, got pane=%d ok=%v", paneID, ok)
	}
}

func TestDisplayPanesUIEvents(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	var events []string
	cr.OnUIEvent = func(name string) {
		events = append(events, name)
	}

	if !cr.ShowDisplayPanes() {
		t.Fatal("ShowDisplayPanes should succeed")
	}
	if !cr.HideDisplayPanes() {
		t.Fatal("HideDisplayPanes should report a state change")
	}

	want := []string{proto.UIEventDisplayPanesShown, proto.UIEventDisplayPanesHidden}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events[%d] = %q, want %q", i, events[i], want[i])
		}
	}
}

func TestChooseWindowOverlayDisplayOnly(t *testing.T) {
	t.Parallel()

	cr := buildMultiWindowRenderer(t)
	if !cr.ShowChooser(chooserModeWindow) {
		t.Fatal("ShowChooser window should succeed")
	}
	cr.RenderDiff()

	display := cr.CaptureDisplay()
	if !strings.Contains(display, "choose-window") || !strings.Contains(display, "1:editor") {
		t.Fatalf("display capture should include chooser overlay, got:\n%s", display)
	}

	plain := cr.Capture(true)
	if strings.Contains(plain, "choose-window") {
		t.Fatalf("plain capture should not include chooser overlay, got:\n%s", plain)
	}
}

func TestChooseTreeFilterAndSelection(t *testing.T) {
	t.Parallel()

	cr := buildMultiWindowRenderer(t)
	if !cr.ShowChooser(chooserModeTree) {
		t.Fatal("ShowChooser tree should succeed")
	}
	cr.HandleChooserInput([]byte("gpu"))

	overlay := cr.chooserOverlay()
	if overlay == nil {
		t.Fatal("chooser overlay should be active")
	}
	if len(overlay.Rows) < 2 {
		t.Fatalf("filtered rows = %+v, want grouped window + pane rows", overlay.Rows)
	}
	if overlay.Rows[1].Text != "  * pane-2 @gpu-box train" && overlay.Rows[1].Text != "    pane-2 @gpu-box train" {
		t.Fatalf("unexpected filtered pane row: %+v", overlay.Rows[1])
	}

	cmd := cr.selectChooser()
	if cmd.command != "select-window" || len(cmd.args) != 1 || cmd.args[0] != "1" {
		t.Fatalf("default filtered selection should land on parent window, got %+v", cmd)
	}
}

func TestChooseTreeNavigationSelectsPane(t *testing.T) {
	t.Parallel()

	cr := buildMultiWindowRenderer(t)
	if !cr.ShowChooser(chooserModeTree) {
		t.Fatal("ShowChooser tree should succeed")
	}
	cr.HandleChooserInput([]byte("pane-3"))
	cr.HandleChooserInput([]byte("j"))
	cmd := cr.selectChooser()
	if cmd.command != "focus" || len(cmd.args) != 1 || cmd.args[0] != "pane-3" {
		t.Fatalf("pane selection = %+v, want focus pane-3", cmd)
	}
}

func TestChooserUIEvents(t *testing.T) {
	t.Parallel()

	cr := buildMultiWindowRenderer(t)
	var events []string
	cr.OnUIEvent = func(name string) {
		events = append(events, name)
	}

	if !cr.ShowChooser(chooserModeWindow) {
		t.Fatal("ShowChooser window should succeed")
	}
	if !cr.HideChooser() {
		t.Fatal("HideChooser should report a state change")
	}

	want := []string{proto.UIEventChooseWindowShown, proto.UIEventChooseWindowHidden}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events[%d] = %q, want %q", i, events[i], want[i])
		}
	}
}

func TestHandleLayoutClearsDisplayPanesEmitsHidden(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	var events []string
	cr.OnUIEvent = func(name string) {
		events = append(events, name)
	}
	if !cr.ShowDisplayPanes() {
		t.Fatal("ShowDisplayPanes should succeed")
	}

	cr.HandleLayout(&proto.LayoutSnapshot{
		SessionName:  "test",
		ActivePaneID: 1,
		Width:        80,
		Height:       23,
		Root: proto.CellSnapshot{
			X: 0, Y: 0, W: 80, H: 23,
			Dir: int(mux.SplitVertical),
			Children: []proto.CellSnapshot{
				{X: 0, Y: 0, W: 39, H: 23, IsLeaf: true, Dir: -1, PaneID: 1},
				{X: 40, Y: 0, W: 39, H: 23, IsLeaf: true, Dir: -1, PaneID: 2},
			},
		},
		Panes: []proto.PaneSnapshot{
			{ID: 1, Name: "pane-1", Host: "local", Color: "f5e0dc"},
			{ID: 2, Name: "pane-2", Host: "local", Color: "f2cdcd"},
		},
	})

	if len(events) != 2 {
		t.Fatalf("events = %v, want shown+hidden", events)
	}
	if events[0] != proto.UIEventDisplayPanesShown || events[1] != proto.UIEventDisplayPanesHidden {
		t.Fatalf("events = %v, want [%q %q]", events, proto.UIEventDisplayPanesShown, proto.UIEventDisplayPanesHidden)
	}
}

func TestHandleCaptureRequest(t *testing.T) {
	t.Parallel()
	cr := buildTestRenderer(t)

	// Plain text
	resp := cr.HandleCaptureRequest([]string{}, nil)
	if resp.Type != proto.MsgTypeCaptureResponse {
		t.Errorf("type: got %d, want %d", resp.Type, proto.MsgTypeCaptureResponse)
	}
	if resp.CmdOutput == "" {
		t.Error("plain capture should produce output")
	}

	// JSON
	resp = cr.HandleCaptureRequest([]string{"--format", "json"}, nil)
	if resp.CmdErr != "" {
		t.Errorf("JSON capture error: %s", resp.CmdErr)
	}
	if !strings.Contains(resp.CmdOutput, `"session"`) {
		t.Error("JSON capture should contain session field")
	}

	// Single pane
	resp = cr.HandleCaptureRequest([]string{"pane-1"}, nil)
	if !strings.Contains(resp.CmdOutput, "hello from pane 1") {
		t.Error("single pane capture should contain pane content")
	}

	// Color map
	resp = cr.HandleCaptureRequest([]string{"--colors"}, nil)
	if resp.CmdErr != "" {
		t.Errorf("color map error: %s", resp.CmdErr)
	}

	// Mutual exclusivity
	resp = cr.HandleCaptureRequest([]string{"--ansi", "--colors"}, nil)
	if resp.CmdErr == "" {
		t.Error("--ansi + --colors should error")
	}

	// Nonexistent pane
	resp = cr.HandleCaptureRequest([]string{"nope"}, nil)
	if resp.CmdErr == "" {
		t.Error("nonexistent pane should error")
	}

	// --colors with pane ref
	resp = cr.HandleCaptureRequest([]string{"--colors", "pane-1"}, nil)
	if resp.CmdErr == "" {
		t.Error("--colors with pane ref should error")
	}
}

func TestHandleCaptureRequest_DisplayFlag(t *testing.T) {
	t.Parallel()
	cr := buildTestRenderer(t)

	// --display before any diff render returns fallback message.
	resp := cr.HandleCaptureRequest([]string{"--display"}, nil)
	if resp.CmdErr != "" {
		t.Errorf("--display error: %s", resp.CmdErr)
	}
	if !strings.Contains(resp.CmdOutput, "no previous grid") {
		t.Errorf("--display before render should show fallback, got: %q", resp.CmdOutput)
	}

	// After a diff render, --display returns grid content.
	cr.RenderDiff()
	resp = cr.HandleCaptureRequest([]string{"--display"}, nil)
	if resp.CmdErr != "" {
		t.Errorf("--display error: %s", resp.CmdErr)
	}
	if !strings.Contains(resp.CmdOutput, "pane-1") {
		t.Errorf("--display should contain pane status, got: %q", resp.CmdOutput)
	}

	// --display is mutually exclusive with other flags.
	for _, args := range [][]string{
		{"--display", "--ansi"},
		{"--display", "--colors"},
		{"--display", "--format", "json"},
		{"--display", "pane-1"},
	} {
		resp = cr.HandleCaptureRequest(args, nil)
		if resp.CmdErr == "" {
			t.Errorf("--display with %v should error", args[1:])
		}
	}
}

func TestRenderCoalesced_FullRenderMode(t *testing.T) {
	// Cannot use t.Parallel — t.Setenv requires sequential execution.
	t.Setenv("AMUX_RENDER", "full")

	cr := buildTestRenderer(t)
	msgCh := make(chan *RenderMsg, 1)

	var rendered string
	msgCh <- &RenderMsg{
		Typ:    RenderMsgPaneOutput,
		PaneID: 1,
		Data:   []byte("test output"),
	}

	done := make(chan struct{})
	go func() {
		cr.RenderCoalesced(msgCh, func(s string) {
			rendered = s
		})
		close(done)
	}()

	// Let the render timer fire, then signal exit.
	<-time.After(50 * time.Millisecond)
	msgCh <- &RenderMsg{Typ: RenderMsgExit}
	<-done

	if rendered == "" {
		t.Fatal("AMUX_RENDER=full should produce output")
	}
}

func TestRescaleLayoutForSmallerClient(t *testing.T) {
	t.Parallel()

	// Client terminal is 40×12, but server layout is 80×23 (the larger client).
	cr := NewClientRenderer(40, 12)
	cr.HandleLayout(twoPane80x23())
	cr.HandlePaneOutput(1, []byte("hello from pane 1"))
	cr.HandlePaneOutput(2, []byte("hello from pane 2"))

	// Both pane status lines should appear in the plain text capture.
	text := cr.Capture(true)
	if !strings.Contains(text, "pane-1") {
		t.Errorf("should contain pane-1 status line\ncapture:\n%s", text)
	}
	if !strings.Contains(text, "pane-2") {
		t.Errorf("should contain pane-2 status line\ncapture:\n%s", text)
	}

	// JSON positions should fit within client bounds (40 wide, 11 layout height).
	jsonOut := cr.CaptureJSON(nil)
	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(jsonOut), &capture); err != nil {
		t.Fatalf("JSON parse: %v", err)
	}
	if len(capture.Panes) != 2 {
		t.Fatalf("panes: got %d, want 2", len(capture.Panes))
	}
	clientLayoutH := 12 - render.GlobalBarHeight
	for _, p := range capture.Panes {
		pos := p.Position
		if pos == nil {
			t.Errorf("pane %s: no position", p.Name)
			continue
		}
		if pos.X+pos.Width > 40 {
			t.Errorf("pane %s: right edge %d exceeds client width 40", p.Name, pos.X+pos.Width)
		}
		if pos.Y+pos.Height > clientLayoutH {
			t.Errorf("pane %s: bottom edge %d exceeds layout height %d", p.Name, pos.Y+pos.Height, clientLayoutH)
		}
	}
}
