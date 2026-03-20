package client

import (
	"encoding/json"
	"fmt"
	"reflect"
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

func threePane80x23() *proto.LayoutSnapshot {
	root := proto.CellSnapshot{
		X: 0, Y: 0, W: 80, H: 23,
		Dir: int(mux.SplitVertical),
		Children: []proto.CellSnapshot{
			{X: 0, Y: 0, W: 26, H: 23, IsLeaf: true, Dir: -1, PaneID: 1},
			{X: 27, Y: 0, W: 26, H: 23, IsLeaf: true, Dir: -1, PaneID: 2},
			{X: 54, Y: 0, W: 26, H: 23, IsLeaf: true, Dir: -1, PaneID: 3},
		},
	}
	panes := []proto.PaneSnapshot{
		{ID: 1, Name: "pane-1", Host: "local", Color: "f5e0dc"},
		{ID: 2, Name: "pane-2", Host: "local", Color: "f2cdcd"},
		{ID: 3, Name: "pane-3", Host: "local", Color: "cba6f7"},
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

func assertClientEffectKinds(t *testing.T, effects []clientEffect, want []clientEffectKind) {
	t.Helper()

	got := make([]clientEffectKind, len(effects))
	for i := range effects {
		got[i] = effects[i].kind
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effect kinds = %v, want %v", got, want)
	}
}

func multiWindow80x23Zoomed(windowID, paneID uint32) *proto.LayoutSnapshot {
	snap := multiWindow80x23()
	for i := range snap.Windows {
		if snap.Windows[i].ID == windowID {
			snap.Windows[i].ZoomedPaneID = paneID
			if snap.ActiveWindowID == windowID {
				snap.ZoomedPaneID = paneID
				snap.ActivePaneID = paneID
				snap.Root = snap.Windows[i].Root
			}
		}
	}
	return snap
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

func TestClientRendererCaptureJSONIncludesChooserAndInputBusy(t *testing.T) {
	t.Parallel()
	cr := buildMultiWindowRenderer(t)

	if !cr.ShowChooser(chooserModeWindow) {
		t.Fatal("ShowChooser window should succeed")
	}
	cr.SetInputIdle(false)

	out := cr.CaptureJSON(nil)
	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		t.Fatalf("JSON parse: %v\nraw: %s", err, out)
	}
	if capture.UI == nil {
		t.Fatal("capture UI state should be present")
	}
	if capture.UI.Chooser != string(chooserModeWindow) {
		t.Fatalf("chooser = %q, want %q", capture.UI.Chooser, chooserModeWindow)
	}
	if capture.UI.InputIdle {
		t.Fatal("input_idle = true, want false")
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

func TestClientRendererZoomedCopyModeSurvivesMetadataOnlyLayout(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(twoPane80x23Zoomed(2))
	cr.HandlePaneOutput(2, []byte("\033[2J\033[Hzoomed copy mode line"))
	cr.EnterCopyMode(2)

	cm := cr.CopyModeForPane(2)
	if cm == nil {
		t.Fatal("pane-2 copy mode missing")
	}
	if got, want := cm.ViewportHeight(), 22; got != want {
		t.Fatalf("zoomed pane-2 copy mode height after initial layout = %d, want %d", got, want)
	}

	idleSnap := twoPane80x23Zoomed(2)
	idleSnap.Panes[1].Idle = true
	idleSnap.Windows[0].Panes[1].Idle = true
	cr.HandleLayout(idleSnap)

	cm = cr.CopyModeForPane(2)
	if cm == nil {
		t.Fatal("pane-2 copy mode missing after idle layout")
	}
	if got, want := cm.ViewportHeight(), 22; got != want {
		t.Fatalf("zoomed pane-2 copy mode height after idle layout = %d, want %d", got, want)
	}

	if got, want := cr.CapturePaneText(2, false), "zoomed copy mode line"; !strings.Contains(got, want) {
		t.Fatalf("pane-2 text after idle layout = %q, want substring %q", got, want)
	}
}

func TestClientRendererZoomedPaneSurvivesMetadataOnlyLayoutMultiWindow(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(multiWindow80x23Zoomed(1, 2))

	const wideLine = "multi-window zoomed pane line that should remain wide after idle"
	cr.HandlePaneOutput(2, []byte("\033[2J\033[H"+wideLine))

	emu, ok := cr.Emulator(2)
	if !ok {
		t.Fatal("pane-2 emulator missing")
	}
	if w, h := emu.Size(); w != 80 || h != 22 {
		t.Fatalf("zoomed pane-2 size after initial multi-window layout = %dx%d, want 80x22", w, h)
	}

	idleSnap := multiWindow80x23Zoomed(1, 2)
	idleSnap.Windows[0].Panes[1].Idle = true
	cr.HandleLayout(idleSnap)

	emu, ok = cr.Emulator(2)
	if !ok {
		t.Fatal("pane-2 emulator missing after multi-window idle layout")
	}
	if w, h := emu.Size(); w != 80 || h != 22 {
		t.Fatalf("zoomed pane-2 size after multi-window idle layout = %dx%d, want 80x22", w, h)
	}

	lines := strings.Split(cr.CapturePaneText(2, false), "\n")
	if len(lines) == 0 || lines[0] != wideLine {
		t.Fatalf("pane-2 first line after multi-window idle layout = %q, want %q", lines[0], wideLine)
	}
}

func TestRescaleZoomedPaneForSmallerClient(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(40, 12)
	cr.HandleLayout(twoPane80x23Zoomed(2))

	emu, ok := cr.Emulator(2)
	if !ok {
		t.Fatal("pane-2 emulator missing")
	}
	if w, h := emu.Size(); w != 40 || h != 10 {
		t.Fatalf("zoomed pane-2 emulator size on smaller client = %dx%d, want 40x10", w, h)
	}

	const wideLine = "1234567890123456789012345678901234567890"
	cr.HandlePaneOutput(2, []byte("\033[2J\033[H"+wideLine))

	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(cr.CaptureJSON(nil)), &capture); err != nil {
		t.Fatalf("JSON parse: %v", err)
	}
	if len(capture.Panes) != 1 {
		t.Fatalf("zoomed smaller-client capture panes = %d, want 1", len(capture.Panes))
	}
	pos := capture.Panes[0].Position
	if pos == nil {
		t.Fatal("zoomed pane position missing")
	}
	if pos.Width != 40 || pos.Height != 11 {
		t.Fatalf("zoomed pane position = %dx%d, want 40x11", pos.Width, pos.Height)
	}
	if got := capture.Panes[0].Content[0]; got != wideLine {
		t.Fatalf("zoomed pane first line on smaller client = %q, want %q", got, wideLine)
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

func TestCommandFeedbackAppearsInDisplayCapture(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.ShowCommandError("cannot minimize: pane has no stacked siblings")

	if got := cr.RenderDiff(); got == "" {
		t.Fatal("RenderDiff with command feedback should produce output")
	}

	display := cr.CaptureDisplay()
	if !strings.Contains(display, "cannot minimize: pane has no stacked siblings") {
		t.Fatalf("display capture should contain command feedback, got:\n%s", display)
	}
}

func TestHandleLayoutClearsCommandFeedback(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.ShowCommandError("cannot minimize: pane has no stacked siblings")
	cr.RenderDiff()

	cr.HandleLayout(twoPane80x23())
	cr.RenderDiff()

	display := cr.CaptureDisplay()
	if strings.Contains(display, "cannot minimize: pane has no stacked siblings") {
		t.Fatalf("layout update should clear command feedback, got:\n%s", display)
	}
}

func TestHandleRenderMsgLayoutReturnsEffects(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	if !cr.ShowDisplayPanes() {
		t.Fatal("ShowDisplayPanes should succeed")
	}

	effects := cr.handleRenderMsg(&RenderMsg{Typ: RenderMsgLayout, Layout: threePane80x23()})
	assertClientEffectKinds(t, effects, []clientEffectKind{
		clientEffectEmitUIEvents,
		clientEffectClearPrevGrid,
		clientEffectStopScheduledRender,
		clientEffectRenderNow,
	})
	if !reflect.DeepEqual(effects[0].uiEvents, []string{proto.UIEventDisplayPanesHidden}) {
		t.Fatalf("ui events = %v, want [%q]", effects[0].uiEvents, proto.UIEventDisplayPanesHidden)
	}
	if cr.DisplayPanesActive() {
		t.Fatal("display panes should be cleared after structural layout change")
	}
}

func TestHandleRenderMsgPaneOutputSchedulesRender(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)

	effects := cr.handleRenderMsg(&RenderMsg{Typ: RenderMsgPaneOutput, PaneID: 1, Data: []byte("more output")})
	assertClientEffectKinds(t, effects, []clientEffectKind{clientEffectScheduleRender})
}

func TestHandleRenderMsgCopyModeReturnsImmediateRenderEffects(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)

	effects := cr.handleRenderMsg(&RenderMsg{Typ: RenderMsgCopyMode, PaneID: 1})
	assertClientEffectKinds(t, effects, []clientEffectKind{
		clientEffectEmitUIEvents,
		clientEffectStopScheduledRender,
		clientEffectRenderNow,
	})
	if !reflect.DeepEqual(effects[0].uiEvents, []string{proto.UIEventCopyModeShown}) {
		t.Fatalf("ui events = %v, want [%q]", effects[0].uiEvents, proto.UIEventCopyModeShown)
	}
	if !cr.InCopyMode(1) {
		t.Fatal("pane-1 should be in copy mode")
	}
}

func TestHandleRenderMsgCommandErrorReturnsBellAndRenderEffects(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)

	effects := cr.handleRenderMsg(&RenderMsg{Typ: RenderMsgCmdError, Text: "cannot minimize: pane has no stacked siblings"})
	assertClientEffectKinds(t, effects, []clientEffectKind{
		clientEffectStopScheduledRender,
		clientEffectBell,
		clientEffectRenderNow,
	})

	effects = cr.handleRenderMsg(&RenderMsg{Typ: RenderMsgCmdError, Text: " \t "})
	if len(effects) != 0 {
		t.Fatalf("blank command error should produce no effects, got %v", effects)
	}
}

func TestHandleRenderMsgExitRendersOnlyWhenDirty(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)

	effects := cr.handleRenderMsg(&RenderMsg{Typ: RenderMsgExit})
	assertClientEffectKinds(t, effects, []clientEffectKind{
		clientEffectRenderNow,
		clientEffectExit,
	})

	cr.RenderDiff()

	effects = cr.handleRenderMsg(&RenderMsg{Typ: RenderMsgExit})
	assertClientEffectKinds(t, effects, []clientEffectKind{clientEffectExit})
}

func TestToggleMinimizeBlockedReasonVerticalSplit(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	if got := cr.toggleMinimizeBlockedReason(); got != minimizeLeftRightSplitReason {
		t.Fatalf("blocked reason = %q, want %q", got, minimizeLeftRightSplitReason)
	}
}

func TestRenderCoalescedCommandErrorShowsFeedback(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	msgCh := make(chan *RenderMsg, 2)

	var rendered strings.Builder
	done := make(chan struct{})
	go func() {
		cr.RenderCoalesced(msgCh, func(s string) {
			rendered.WriteString(s)
		})
		close(done)
	}()

	msgCh <- &RenderMsg{Typ: RenderMsgCmdError, Text: "cannot minimize: pane has no stacked siblings"}
	msgCh <- &RenderMsg{Typ: RenderMsgExit}
	close(msgCh)
	<-done

	if !strings.Contains(rendered.String(), "\a") {
		t.Fatalf("command error render should ring bell, got %q", rendered.String())
	}
	if !strings.Contains(cr.CaptureDisplay(), "cannot minimize: pane has no stacked siblings") {
		t.Fatalf("display capture should contain command feedback, got:\n%s", cr.CaptureDisplay())
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

func TestPrefixMessageDisplayOnly(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.ShowPrefixMessage("No binding for C-a f")
	cr.RenderDiff()

	display := cr.CaptureDisplay()
	if !strings.Contains(display, "No binding for C-a f") {
		t.Fatalf("display capture should include prefix message, got:\n%s", display)
	}

	plain := cr.Capture(true)
	if strings.Contains(plain, "No binding for C-a f") {
		t.Fatalf("plain capture should not include prefix message, got:\n%s", plain)
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

func TestInputIdleUIEvents(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	var events []string
	cr.OnUIEvent = func(name string) {
		events = append(events, name)
	}

	cr.SetInputIdle(true)
	cr.SetInputIdle(false)
	cr.SetInputIdle(false)
	cr.SetInputIdle(true)

	want := []string{proto.UIEventInputBusy, proto.UIEventInputIdle}
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

	// Send a structurally different layout (3 panes instead of 2) so that
	// HandleLayout detects a structure change and clears the overlay.
	cr.HandleLayout(&proto.LayoutSnapshot{
		SessionName:  "test",
		ActivePaneID: 1,
		Width:        80,
		Height:       23,
		Root: proto.CellSnapshot{
			X: 0, Y: 0, W: 80, H: 23,
			Dir: int(mux.SplitVertical),
			Children: []proto.CellSnapshot{
				{X: 0, Y: 0, W: 26, H: 23, IsLeaf: true, Dir: -1, PaneID: 1},
				{X: 27, Y: 0, W: 26, H: 23, IsLeaf: true, Dir: -1, PaneID: 2},
				{X: 54, Y: 0, W: 26, H: 23, IsLeaf: true, Dir: -1, PaneID: 3},
			},
		},
		Panes: []proto.PaneSnapshot{
			{ID: 1, Name: "pane-1", Host: "local", Color: "f5e0dc"},
			{ID: 2, Name: "pane-2", Host: "local", Color: "f2cdcd"},
			{ID: 3, Name: "pane-3", Host: "local", Color: "cba6f7"},
		},
	})

	if len(events) != 2 {
		t.Fatalf("events = %v, want shown+hidden", events)
	}
	if events[0] != proto.UIEventDisplayPanesShown || events[1] != proto.UIEventDisplayPanesHidden {
		t.Fatalf("events = %v, want [%q %q]", events, proto.UIEventDisplayPanesShown, proto.UIEventDisplayPanesHidden)
	}
}

func TestHandleLayoutPreservesDisplayPanesOnNonStructuralChange(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	var events []string
	cr.OnUIEvent = func(name string) {
		events = append(events, name)
	}
	if !cr.ShowDisplayPanes() {
		t.Fatal("ShowDisplayPanes should succeed")
	}

	// Re-send the same layout (non-structural change) — overlay must survive.
	cr.HandleLayout(twoPane80x23())

	if len(events) != 1 {
		t.Fatalf("events = %v, want only shown (no hidden)", events)
	}
	if events[0] != proto.UIEventDisplayPanesShown {
		t.Fatalf("events[0] = %q, want %q", events[0], proto.UIEventDisplayPanesShown)
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

	resp = cr.HandleCaptureRequest([]string{"--format", "json", "nope"}, nil)
	if resp.CmdErr == "" {
		t.Fatal("JSON capture with nonexistent pane should error")
	}
	if !strings.Contains(resp.CmdErr, `pane "nope" not found`) {
		t.Fatalf("CmdErr = %q, want pane not found", resp.CmdErr)
	}
}

func TestClientRendererCopyModeUsesPaneHistory(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.HandlePaneHistory(1, []string{"old-1", "old-2"})

	cr.EnterCopyMode(1)
	cm := cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("copy mode should exist for pane-1")
	}
	if got := cm.LineText(0); got != "old-1" {
		t.Fatalf("LineText(0) = %q, want %q", got, "old-1")
	}
	if got := cm.LineText(1); got != "old-2" {
		t.Fatalf("LineText(1) = %q, want %q", got, "old-2")
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
