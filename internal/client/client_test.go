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
		{ID: 1, Name: "pane-1", Host: "local", Color: "f5e0dc", ColumnIndex: 0},
		{ID: 2, Name: "pane-2", Host: "local", Color: "f2cdcd", ColumnIndex: 1},
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
		{ID: 1, Name: "pane-1", Host: "local", Color: "f5e0dc", ColumnIndex: 0},
		{ID: 2, Name: "pane-2", Host: "local", Color: "f2cdcd", ColumnIndex: 1},
		{ID: 3, Name: "pane-3", Host: "local", Color: "cba6f7", ColumnIndex: 2},
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

func duplicateNameTwoPane80x23(name string) *proto.LayoutSnapshot {
	snap := twoPane80x23()
	for i := range snap.Panes {
		snap.Panes[i].Name = name
	}
	return snap
}

func singlePane20xN(height int) *proto.LayoutSnapshot {
	root := proto.CellSnapshot{
		X: 0, Y: 0, W: 20, H: height,
		IsLeaf: true, Dir: -1, PaneID: 1,
	}
	panes := []proto.PaneSnapshot{
		{ID: 1, Name: "pane-1", Host: "local", Color: "f5e0dc", ColumnIndex: 0},
	}
	return &proto.LayoutSnapshot{
		SessionName:  "test",
		ActivePaneID: 1,
		Width:        20,
		Height:       height,
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

func singlePane20x3() *proto.LayoutSnapshot {
	return singlePane20xN(3)
}

func singlePane20x5() *proto.LayoutSnapshot {
	return singlePane20xN(5)
}

// buildTestRenderer creates a ClientRenderer with two panes in a vertical split.
func buildTestRenderer(t *testing.T) *ClientRenderer {
	t.Helper()
	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(twoPane80x23())
	cr.HandlePaneOutput(1, []byte("hello from pane 1"))
	return cr
}

func TestClientRendererCapabilities(t *testing.T) {
	t.Parallel()

	caps := proto.ClientCapabilities{
		KittyKeyboard:  true,
		Hyperlinks:     true,
		PromptMarkers:  true,
		CursorMetadata: true,
	}

	cr := NewClientRenderer(80, 24)
	cr.SetCapabilities(caps)

	if got := cr.Capabilities(); got != caps {
		t.Fatalf("Capabilities() = %+v, want %+v", got, caps)
	}
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

func collectClientEffectUIEvents(effects []clientEffect) []string {
	var uiEvents []string
	for _, effect := range effects {
		if effect.kind == clientEffectEmitUIEvents {
			uiEvents = append(uiEvents, effect.uiEvents...)
		}
	}
	return uiEvents
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

	snap := twoPane80x23()
	snap.Panes[1].Host = "test-remote"
	snap.Panes[1].ConnStatus = "connected"
	snap.Windows[0].Panes[1] = snap.Panes[1]
	cr.HandleLayout(snap)

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

func TestClientRendererCaptureJSONReturnsErrorObjectWithoutLayout(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)

	var capture struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	out := cr.CaptureJSON(nil)
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		t.Fatalf("JSON parse: %v\nraw: %s", err, out)
	}
	if capture.Error == nil {
		t.Fatalf("capture should return an error object when layout is unavailable, got: %s", out)
	}
	if capture.Error.Code != "state_unavailable" {
		t.Fatalf("error code = %q, want state_unavailable", capture.Error.Code)
	}
	if capture.Error.Message == "" {
		t.Fatal("error message should be non-empty")
	}
}

func TestRendererCaptureJSONReturnsErrorObjectWithoutLayout(t *testing.T) {
	t.Parallel()

	r := NewWithScrollback(80, 24, mux.DefaultScrollbackLines)

	var capture struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	out := r.CaptureJSON(nil)
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		t.Fatalf("JSON parse: %v\nraw: %s", err, out)
	}
	if capture.Error == nil {
		t.Fatalf("capture should return an error object when layout is unavailable, got: %s", out)
	}
	if capture.Error.Code != "state_unavailable" {
		t.Fatalf("error code = %q, want state_unavailable", capture.Error.Code)
	}
	if capture.Error.Message == "" {
		t.Fatal("error message should be non-empty")
	}
}

func TestRendererCaptureJSONValueMatchesCaptureJSON(t *testing.T) {
	t.Parallel()
	cr := buildTestRenderer(t)

	status := map[uint32]proto.PaneAgentStatus{
		1: {Idle: true, CurrentCommand: "bash", ChildPIDs: []int{}},
	}
	capture, ok := cr.renderer.captureJSONValue(status)
	if !ok {
		t.Fatal("captureJSONValue returned no layout")
	}

	if got, want := marshalIndented(capture), cr.renderer.CaptureJSON(status); got != want {
		t.Fatalf("captureJSONValue output mismatch\n got: %s\nwant: %s", got, want)
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

func TestClientRendererCapturePaneTextStripsHyperlinksWhenUnsupported(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(twoPane80x23())
	cr.HandlePaneOutput(1, []byte("\033]8;;https://example.com\033\\test-link\033]8;;\033\\"))

	ansi := cr.CapturePaneText(1, true)
	if strings.Contains(ansi, "\033]8;") {
		t.Fatalf("CapturePaneText should strip OSC 8 when hyperlinks are unsupported, got %q", ansi)
	}
	if !strings.Contains(ansi, "test-link") {
		t.Fatalf("CapturePaneText should preserve visible link text, got %q", ansi)
	}
}

func TestClientRendererCapturePaneTextPreservesHyperlinksWhenSupported(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	cr.SetCapabilities(proto.ClientCapabilities{Hyperlinks: true})
	cr.HandleLayout(twoPane80x23())
	cr.HandlePaneOutput(1, []byte("\033]8;;https://example.com\033\\test-link\033]8;;\033\\"))

	ansi := cr.CapturePaneText(1, true)
	if !strings.Contains(ansi, "\033]8;") {
		t.Fatalf("CapturePaneText should preserve OSC 8 when hyperlinks are supported, got %q", ansi)
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

	var missing struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	empty := cr.CapturePaneJSON(999, nil)
	if err := json.Unmarshal([]byte(empty), &missing); err != nil {
		t.Fatalf("missing pane JSON parse: %v", err)
	}
	if missing.Error == nil || missing.Error.Code != "state_unavailable" {
		t.Errorf("nonexistent pane should return a state_unavailable error, got %q", empty)
	}
}

func TestClientRendererCapturePaneJSONReturnsErrorObjectWithoutLayout(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)

	var pane struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	out := cr.CapturePaneJSON(1, nil)
	if err := json.Unmarshal([]byte(out), &pane); err != nil {
		t.Fatalf("JSON parse: %v\nraw: %s", err, out)
	}
	if pane.Error == nil {
		t.Fatalf("pane capture should return an error object when state is unavailable, got: %s", out)
	}
	if pane.Error.Code != "state_unavailable" {
		t.Fatalf("error code = %q, want state_unavailable", pane.Error.Code)
	}
	if pane.Error.Message == "" {
		t.Fatal("error message should be non-empty")
	}
}

func TestRendererCapturePaneJSONReturnsErrorObjectWithoutLayout(t *testing.T) {
	t.Parallel()

	r := NewWithScrollback(80, 24, mux.DefaultScrollbackLines)

	var pane struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	out := r.CapturePaneJSON(1, nil)
	if err := json.Unmarshal([]byte(out), &pane); err != nil {
		t.Fatalf("JSON parse: %v\nraw: %s", err, out)
	}
	if pane.Error == nil {
		t.Fatalf("pane capture should return an error object when state is unavailable, got: %s", out)
	}
	if pane.Error.Code != "state_unavailable" {
		t.Fatalf("error code = %q, want state_unavailable", pane.Error.Code)
	}
	if pane.Error.Message == "" {
		t.Fatal("error message should be non-empty")
	}
}

func TestRendererCapturePaneValueMatchesCapturePaneJSON(t *testing.T) {
	t.Parallel()
	cr := buildTestRenderer(t)

	status := map[uint32]proto.PaneAgentStatus{
		1: {Idle: true, CurrentCommand: "bash", ChildPIDs: []int{}},
	}
	pane, ok := cr.renderer.capturePaneValue(1, status)
	if !ok {
		t.Fatal("capturePaneValue returned no pane")
	}

	if got, want := marshalIndented(pane), cr.renderer.CapturePaneJSON(1, status); got != want {
		t.Fatalf("capturePaneValue output mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestClientRendererCapturePaneJSONIncludesCopyMode(t *testing.T) {
	t.Parallel()
	cr := buildTestRenderer(t)

	cr.EnterCopyMode(1)

	var pane proto.CapturePane
	if err := json.Unmarshal([]byte(cr.CapturePaneJSON(1, nil)), &pane); err != nil {
		t.Fatalf("JSON parse: %v", err)
	}
	if !pane.CopyMode {
		t.Fatal("copy_mode = false, want true")
	}
}

func TestClientRendererZoomedPaneSurvivesMetadataOnlyLayout(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(twoPane80x23Zoomed(2))

	const wideLine = "012345678901234567890123456789012345678901234567890123456789"
	cr.HandlePaneOutput(2, []byte("\033[2J\033[H"+wideLine))

	w, h, ok := cr.renderer.PaneSize(2)
	if !ok {
		t.Fatal("pane-2 emulator missing")
	}
	if w != 80 || h != 22 {
		t.Fatalf("zoomed pane-2 size after initial layout = %dx%d, want 80x22", w, h)
	}

	idleSnap := twoPane80x23Zoomed(2)
	idleSnap.Panes[1].Idle = true
	idleSnap.Windows[0].Panes[1].Idle = true
	cr.HandleLayout(idleSnap)

	w, h, ok = cr.renderer.PaneSize(2)
	if !ok {
		t.Fatal("pane-2 emulator missing after idle layout")
	}
	if w != 80 || h != 22 {
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

	w, h, ok := cr.renderer.PaneSize(2)
	if !ok {
		t.Fatal("pane-2 emulator missing")
	}
	if w != 80 || h != 22 {
		t.Fatalf("zoomed pane-2 size after initial multi-window layout = %dx%d, want 80x22", w, h)
	}

	idleSnap := multiWindow80x23Zoomed(1, 2)
	idleSnap.Windows[0].Panes[1].Idle = true
	cr.HandleLayout(idleSnap)

	w, h, ok = cr.renderer.PaneSize(2)
	if !ok {
		t.Fatal("pane-2 emulator missing after multi-window idle layout")
	}
	if w != 80 || h != 22 {
		t.Fatalf("zoomed pane-2 size after multi-window idle layout = %dx%d, want 80x22", w, h)
	}

	lines := strings.Split(cr.CapturePaneText(2, false), "\n")
	if len(lines) == 0 || lines[0] != wideLine {
		t.Fatalf("pane-2 first line after multi-window idle layout = %q, want %q", lines[0], wideLine)
	}
}

func TestRendererHandleLayoutCallsOnPaneResizeForZoomedPane(t *testing.T) {
	t.Parallel()

	r := NewWithScrollback(80, 24, mux.DefaultScrollbackLines)

	type resizeCall struct {
		paneID uint32
		w      int
		h      int
	}
	var calls []resizeCall
	r.OnPaneResize = func(paneID uint32, w, h int) {
		calls = append(calls, resizeCall{paneID: paneID, w: w, h: h})
	}

	r.HandleLayout(twoPane80x23Zoomed(2))

	if len(calls) != 2 {
		t.Fatalf("OnPaneResize calls = %d, want 2", len(calls))
	}
	if got := calls[0]; got != (resizeCall{paneID: 1, w: 39, h: 22}) {
		t.Fatalf("first OnPaneResize call = %+v, want pane-1 at 39x22", got)
	}
	if got := calls[1]; got != (resizeCall{paneID: 2, w: 80, h: 22}) {
		t.Fatalf("second OnPaneResize call = %+v, want zoomed pane-2 at 80x22", got)
	}

	w, h, ok := r.PaneSize(2)
	if !ok {
		t.Fatal("zoomed pane-2 emulator missing")
	}
	if w != 80 || h != 22 {
		t.Fatalf("zoomed pane-2 size after HandleLayout = %dx%d, want 80x22", w, h)
	}
}

func TestRescaleZoomedPaneForSmallerClient(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(40, 12)
	cr.HandleLayout(twoPane80x23Zoomed(2))

	w, h, ok := cr.renderer.PaneSize(2)
	if !ok {
		t.Fatal("pane-2 emulator missing")
	}
	if w != 40 || h != 10 {
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

	if id, err := cr.ResolvePaneID("1"); err != nil || id != 1 {
		t.Errorf("numeric: got (%d, %v), want (1, nil)", id, err)
	}
	if id, err := cr.ResolvePaneID("pane-2"); err != nil || id != 2 {
		t.Errorf("name: got (%d, %v), want (2, nil)", id, err)
	}
	if _, err := cr.ResolvePaneID("pane-"); err == nil || !strings.Contains(err.Error(), `pane "pane-" not found`) {
		t.Fatalf("prefix resolution error = %v, want pane not found", err)
	}
	if _, err := cr.ResolvePaneID("nonexistent"); err == nil || !strings.Contains(err.Error(), `pane "nonexistent" not found`) {
		t.Fatalf("nonexistent resolution error = %v, want pane not found", err)
	}
}

func TestClientRendererResolvePaneIDRejectsAmbiguousExactNames(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(duplicateNameTwoPane80x23("shared"))

	if _, err := cr.ResolvePaneID("shared"); err == nil || !strings.Contains(err.Error(), `pane "shared" is ambiguous`) {
		t.Fatalf("ResolvePaneID(shared) error = %v, want ambiguous", err)
	}
}

func TestClientRendererActivePaneName(t *testing.T) {
	t.Parallel()

	t.Run("known active pane", func(t *testing.T) {
		t.Parallel()

		cr := buildTestRenderer(t)
		if got := cr.ActivePaneName(); got != "pane-1" {
			t.Fatalf("ActivePaneName() = %q, want %q", got, "pane-1")
		}
	})

	t.Run("unknown without layout", func(t *testing.T) {
		t.Parallel()

		cr := NewClientRenderer(80, 24)
		if got := cr.ActivePaneName(); got != "" {
			t.Fatalf("ActivePaneName() without layout = %q, want empty string", got)
		}
	})
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

func TestCaptureDisplayShowsPaneMetadata(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	snap := singlePane20xN(23)
	snap.Panes[0].TrackedPRs = []proto.TrackedPR{{Number: 42}, {Number: 314}}
	snap.Panes[0].TrackedIssues = []proto.TrackedIssue{{ID: "LAB-339"}}
	snap.Windows[0].Panes[0].TrackedPRs = []proto.TrackedPR{{Number: 42}, {Number: 314}}
	snap.Windows[0].Panes[0].TrackedIssues = []proto.TrackedIssue{{ID: "LAB-339"}}

	cr.HandleLayout(snap)
	cr.HandlePaneOutput(1, []byte("metadata display"))
	cr.RenderDiff()

	display := cr.CaptureDisplay()
	if !strings.Contains(display, "#42, #314, LAB-339") {
		t.Fatalf("display should contain pane metadata, got:\n%s", display)
	}
}

func TestCaptureDisplayShowsLeadPaneStatus(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	snap := twoPane80x23()
	snap.LeadPaneID = 1
	snap.Panes[0].Lead = true
	snap.Windows[0].LeadPaneID = 1
	snap.Windows[0].Panes[0].Lead = true

	cr.HandleLayout(snap)
	cr.HandlePaneOutput(1, []byte("lead display"))
	cr.RenderDiff()

	display := cr.CaptureDisplay()
	for _, want := range []string{"▶", "[pane-1]", "[lead]"} {
		if !strings.Contains(display, want) {
			t.Fatalf("display should contain %q, got:\n%s", want, display)
		}
	}
}

func TestCommandFeedbackAppearsInDisplayCapture(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.ShowCommandError("command failed: pane is invalid")

	if got := cr.RenderDiff(); got == "" {
		t.Fatal("RenderDiff with command feedback should produce output")
	}

	display := cr.CaptureDisplay()
	if !strings.Contains(display, "command failed: pane is invalid") {
		t.Fatalf("display capture should contain command feedback, got:\n%s", display)
	}
}

func TestSessionNoticeAppearsInDisplayCapture(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	snap := twoPane80x23()
	snap.Notice = "takeover badhost (127.0.0.1:1): SSH dial 127.0.0.1:1"
	cr.HandleLayout(snap)

	if got := cr.RenderDiff(); got == "" {
		t.Fatal("RenderDiff with session notice should produce output")
	}

	display := cr.CaptureDisplay()
	if !strings.Contains(display, "takeover badhost") {
		t.Fatalf("display capture should contain session notice, got:\n%s", display)
	}
}

func TestCommandFeedbackOverridesSessionNotice(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	snap := twoPane80x23()
	snap.Notice = "takeover badhost (127.0.0.1:1): SSH dial 127.0.0.1:1"
	cr.HandleLayout(snap)
	cr.ShowCommandError("command failed: pane is invalid")

	if got := cr.RenderDiff(); got == "" {
		t.Fatal("RenderDiff with command feedback and session notice should produce output")
	}

	display := cr.CaptureDisplay()
	if !strings.Contains(display, "command failed: pane is invalid") {
		t.Fatalf("display capture should contain command feedback, got:\n%s", display)
	}
	if strings.Contains(display, "takeover badhost") {
		t.Fatalf("session notice should not override command feedback, got:\n%s", display)
	}
}

func TestStructuralHandleLayoutClearsCommandFeedback(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.ShowCommandError("command failed: pane is invalid")
	cr.RenderDiff()

	cr.HandleLayout(threePane80x23())
	cr.RenderDiff()

	display := cr.CaptureDisplay()
	if strings.Contains(display, "command failed: pane is invalid") {
		t.Fatalf("structural layout update should clear command feedback, got:\n%s", display)
	}
}

func TestHandleRenderMsgEffects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		prepare      func(*testing.T, *ClientRenderer)
		msg          *RenderMsg
		wantKinds    []clientEffectKind
		wantUIEvents []string
		assert       func(*testing.T, *ClientRenderer, []clientEffect)
	}{
		{
			name: "structural layout change clears overlay and repaints immediately",
			prepare: func(t *testing.T, cr *ClientRenderer) {
				if !cr.ShowDisplayPanes() {
					t.Fatal("ShowDisplayPanes should succeed")
				}
				cr.ShowPrefixMessage("No binding for C-a f")
			},
			msg: &RenderMsg{Typ: RenderMsgLayout, Layout: threePane80x23()},
			wantKinds: []clientEffectKind{
				clientEffectEmitUIEvents,
				clientEffectClearPrevGrid,
				clientEffectStopScheduledRender,
				clientEffectRenderNow,
			},
			wantUIEvents: []string{
				proto.UIEventDisplayPanesHidden,
				proto.UIEventPrefixMessageHidden,
			},
			assert: func(t *testing.T, cr *ClientRenderer, _ []clientEffect) {
				t.Helper()
				if cr.DisplayPanesActive() {
					t.Fatal("display panes should be cleared after structural layout change")
				}
				if got := cr.prefixMessage(); got != "" {
					t.Fatalf("structural layout change should clear prefix message, got %q", got)
				}
			},
		},
		{
			name: "non-structural layout change preserves overlay message and skips grid clear",
			prepare: func(t *testing.T, cr *ClientRenderer) {
				if !cr.ShowDisplayPanes() {
					t.Fatal("ShowDisplayPanes should succeed")
				}
				cr.ShowCommandError("command failed")
			},
			msg: &RenderMsg{Typ: RenderMsgLayout, Layout: twoPane80x23()},
			wantKinds: []clientEffectKind{
				clientEffectStopScheduledRender,
				clientEffectRenderNow,
			},
			assert: func(t *testing.T, cr *ClientRenderer, _ []clientEffect) {
				t.Helper()
				if !cr.DisplayPanesActive() {
					t.Fatal("display panes should survive a non-structural layout change")
				}
				if got := cr.prefixMessage(); got != "command failed" {
					t.Fatalf("metadata-only layout update should preserve command feedback, got %q", got)
				}
			},
		},
		{
			name: "pane output preserves message and schedules render",
			prepare: func(_ *testing.T, cr *ClientRenderer) {
				cr.ShowPrefixMessage("No binding for C-a f")
			},
			msg:       &RenderMsg{Typ: RenderMsgPaneOutput, PaneID: 1, Data: []byte("more output")},
			wantKinds: []clientEffectKind{clientEffectScheduleRender},
			assert: func(t *testing.T, cr *ClientRenderer, _ []clientEffect) {
				t.Helper()
				if got := cr.prefixMessage(); got != "No binding for C-a f" {
					t.Fatalf("pane output should preserve the prefix message, got %q", got)
				}
				if !cr.IsDirty() {
					t.Fatal("pane output should leave the renderer dirty until the scheduled render runs")
				}
			},
		},
		{
			name: "copy mode returns immediate render effects",
			msg:  &RenderMsg{Typ: RenderMsgCopyMode, PaneID: 1},
			wantKinds: []clientEffectKind{
				clientEffectEmitUIEvents,
				clientEffectStopScheduledRender,
				clientEffectRenderNow,
			},
			wantUIEvents: []string{proto.UIEventCopyModeShown},
			assert: func(t *testing.T, cr *ClientRenderer, _ []clientEffect) {
				t.Helper()
				if !cr.InCopyMode(1) {
					t.Fatal("pane-1 should be in copy mode")
				}
			},
		},
		{
			name: "additional copy-mode pane renders immediately without re-emitting shown",
			prepare: func(_ *testing.T, cr *ClientRenderer) {
				cr.EnterCopyMode(1)
			},
			msg:          &RenderMsg{Typ: RenderMsgCopyMode, PaneID: 2},
			wantKinds:    []clientEffectKind{clientEffectStopScheduledRender, clientEffectRenderNow},
			wantUIEvents: []string{},
			assert: func(t *testing.T, cr *ClientRenderer, _ []clientEffect) {
				t.Helper()
				if !cr.InCopyMode(1) || !cr.InCopyMode(2) {
					t.Fatal("both panes should be in copy mode after entering a second pane")
				}
			},
		},
		{
			name: "command error trims text and rings bell",
			msg:  &RenderMsg{Typ: RenderMsgCmdError, Text: "  command failed  \n"},
			wantKinds: []clientEffectKind{
				clientEffectStopScheduledRender,
				clientEffectBell,
				clientEffectRenderNow,
			},
			assert: func(t *testing.T, cr *ClientRenderer, _ []clientEffect) {
				t.Helper()
				if got := cr.prefixMessage(); got != "command failed" {
					t.Fatalf("command feedback = %q, want %q", got, "command failed")
				}
			},
		},
		{
			name:      "blank command error is ignored",
			msg:       &RenderMsg{Typ: RenderMsgCmdError, Text: " \t "},
			wantKinds: []clientEffectKind{},
			assert: func(t *testing.T, cr *ClientRenderer, _ []clientEffect) {
				t.Helper()
				if got := cr.prefixMessage(); got != "" {
					t.Fatalf("blank command error should not set command feedback, got %q", got)
				}
			},
		},
		{
			name: "dirty exit renders before exiting",
			msg:  &RenderMsg{Typ: RenderMsgExit},
			wantKinds: []clientEffectKind{
				clientEffectRenderNow,
				clientEffectExit,
			},
		},
		{
			name: "clean exit skips the final render",
			prepare: func(_ *testing.T, cr *ClientRenderer) {
				cr.RenderDiff()
			},
			msg:       &RenderMsg{Typ: RenderMsgExit},
			wantKinds: []clientEffectKind{clientEffectExit},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cr := buildTestRenderer(t)
			if tt.prepare != nil {
				tt.prepare(t, cr)
			}

			effects := cr.handleRenderMsg(tt.msg)

			assertClientEffectKinds(t, effects, tt.wantKinds)
			assertUIEvents(t, collectClientEffectUIEvents(effects), tt.wantUIEvents)
			if tt.assert != nil {
				tt.assert(t, cr, effects)
			}
		})
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

	msgCh <- &RenderMsg{Typ: RenderMsgCmdError, Text: "command failed: pane is invalid"}
	msgCh <- &RenderMsg{Typ: RenderMsgExit}
	close(msgCh)
	<-done

	if !strings.Contains(rendered.String(), "\a") {
		t.Fatalf("command error render should ring bell, got %q", rendered.String())
	}
	if !strings.Contains(cr.CaptureDisplay(), "command failed: pane is invalid") {
		t.Fatalf("display capture should contain command feedback, got:\n%s", cr.CaptureDisplay())
	}
}

func TestRenderCoalescedLocalActionReplySentBeforeExit(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	msgCh := make(chan *RenderMsg, 1)
	done := make(chan struct{})
	go func() {
		cr.RenderCoalesced(msgCh, func(string) {})
		close(done)
	}()

	replyCh := make(chan string, 1)
	go func() {
		replyCh <- callLocalRenderAction[string](cr, msgCh, func(*ClientRenderer) localRenderResult {
			return localRenderResult{
				effects: []clientEffect{{kind: clientEffectExit}},
				value:   "ok",
			}
		})
	}()

	select {
	case got := <-replyCh:
		if got != "ok" {
			t.Fatalf("reply = %q, want %q", got, "ok")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("local action reply blocked on exit")
	}

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("render loop did not exit after local action exit effect")
	}
}

func TestRenderCoalescedPaneOutputRendersImmediatelyAfterIdle(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.renderFrameInterval = 250 * time.Millisecond
	msgCh := make(chan *RenderMsg, 2)
	rendered := make(chan time.Time, 1)
	done := make(chan struct{})

	go func() {
		cr.RenderCoalesced(msgCh, func(string) {
			select {
			case rendered <- time.Now():
			default:
			}
		})
		close(done)
	}()

	start := time.Now()
	msgCh <- &RenderMsg{Typ: RenderMsgPaneOutput, PaneID: 1, Data: []byte("test output")}

	select {
	case ts := <-rendered:
		if ts.Sub(start) >= 100*time.Millisecond {
			t.Fatalf("first pane output rendered after %v, want immediate render well below frame interval %v", ts.Sub(start), cr.renderFrameInterval)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("first pane output did not render immediately; frame interval is %v", cr.renderFrameInterval)
	}

	msgCh <- &RenderMsg{Typ: RenderMsgExit}
	close(msgCh)
	<-done
}

func TestRenderCoalescedPaneOutputRespectsFrameBudget(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.renderFrameInterval = 50 * time.Millisecond
	msgCh := make(chan *RenderMsg, 4)
	rendered := make(chan time.Time, 4)
	done := make(chan struct{})

	go func() {
		cr.RenderCoalesced(msgCh, func(string) {
			rendered <- time.Now()
		})
		close(done)
	}()

	msgCh <- &RenderMsg{Typ: RenderMsgPaneOutput, PaneID: 1, Data: []byte("first")}
	first := <-rendered

	msgCh <- &RenderMsg{Typ: RenderMsgPaneOutput, PaneID: 1, Data: []byte(" second")}

	select {
	case ts := <-rendered:
		t.Fatalf("second pane output rendered too early after %v", ts.Sub(first))
	case <-time.After(15 * time.Millisecond):
	}

	select {
	case ts := <-rendered:
		if delta := ts.Sub(first); delta < 35*time.Millisecond {
			t.Fatalf("second pane output rendered after %v, want it deferred close to frame interval %v", delta, cr.renderFrameInterval)
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatalf("second pane output did not render within frame interval %v", cr.renderFrameInterval)
	}

	msgCh <- &RenderMsg{Typ: RenderMsgExit}
	close(msgCh)
	<-done
}

func TestRenderCoalescedPrioritizesActivePaneOutputAfterLocalInput(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.renderFrameInterval = 250 * time.Millisecond
	cr.renderPriorityWindow = 250 * time.Millisecond
	msgCh := make(chan *RenderMsg, 4)
	rendered := make(chan time.Time, 4)
	done := make(chan struct{})

	go func() {
		cr.RenderCoalesced(msgCh, func(string) {
			rendered <- time.Now()
		})
		close(done)
	}()

	msgCh <- &RenderMsg{Typ: RenderMsgPaneOutput, PaneID: 2, Data: []byte("background")}
	<-rendered

	cr.MarkLocalInput()
	start := time.Now()
	msgCh <- &RenderMsg{Typ: RenderMsgPaneOutput, PaneID: 1, Data: []byte("typed")}

	select {
	case ts := <-rendered:
		if ts.Sub(start) >= 100*time.Millisecond {
			t.Fatalf("active pane output rendered after %v, want immediate render while priority window %v is active", ts.Sub(start), cr.renderPriorityWindow)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("active pane output did not bypass frame budget after local input")
	}

	msgCh <- &RenderMsg{Typ: RenderMsgExit}
	close(msgCh)
	<-done
}

func TestRenderCoalescedDoesNotPrioritizeBackgroundPaneAfterLocalInput(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.renderFrameInterval = 50 * time.Millisecond
	cr.renderPriorityWindow = 250 * time.Millisecond
	msgCh := make(chan *RenderMsg, 4)
	rendered := make(chan time.Time, 4)
	done := make(chan struct{})

	go func() {
		cr.RenderCoalesced(msgCh, func(string) {
			rendered <- time.Now()
		})
		close(done)
	}()

	msgCh <- &RenderMsg{Typ: RenderMsgPaneOutput, PaneID: 1, Data: []byte("seed")}
	first := <-rendered

	cr.MarkLocalInput()
	msgCh <- &RenderMsg{Typ: RenderMsgPaneOutput, PaneID: 2, Data: []byte("background")}

	select {
	case ts := <-rendered:
		t.Fatalf("background pane output rendered too early after %v", ts.Sub(first))
	case <-time.After(15 * time.Millisecond):
	}

	select {
	case ts := <-rendered:
		if delta := ts.Sub(first); delta < 25*time.Millisecond {
			t.Fatalf("background pane output rendered after %v, want it to remain frame-limited", delta)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("background pane output did not render within frame interval %v", cr.renderFrameInterval)
	}

	msgCh <- &RenderMsg{Typ: RenderMsgExit}
	close(msgCh)
	<-done
}

func TestClientRenderLoopStateShouldRenderNowReturnsFalseWithPendingTimer(t *testing.T) {
	t.Parallel()

	timer := time.NewTimer(time.Hour)
	defer timer.Stop()

	state := clientRenderLoopState{
		renderTimer: timer,
		renderC:     timer.C,
	}
	if state.shouldRenderNow() {
		t.Fatal("shouldRenderNow() = true, want false when a timer is already pending")
	}
}

func TestClientRenderLoopStateScheduleRenderDoesNotReplacePendingTimer(t *testing.T) {
	t.Parallel()

	timer := time.NewTimer(time.Hour)
	defer timer.Stop()

	state := clientRenderLoopState{
		renderTimer: timer,
		renderC:     timer.C,
	}
	state.scheduleRender()

	if state.renderTimer != timer {
		t.Fatal("scheduleRender replaced an existing timer")
	}
	if state.renderC != timer.C {
		t.Fatal("scheduleRender replaced an existing timer channel")
	}
}

func TestClientRenderLoopStateScheduleRenderClampsPastDueDelayToZero(t *testing.T) {
	t.Parallel()

	state := clientRenderLoopState{
		renderFrameInterval: 100 * time.Millisecond,
		lastRender:          time.Now().Add(-200 * time.Millisecond),
	}
	state.scheduleRender()
	defer state.stopScheduledRender()

	select {
	case <-state.renderC:
	case <-time.After(20 * time.Millisecond):
		t.Fatal("scheduleRender did not fire immediately for an overdue render")
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

func TestPrefixMessageUIEvents(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	var events []string
	cr.OnUIEvent = func(name string) {
		events = append(events, name)
	}

	cr.ShowPrefixMessage("No binding for C-a f")
	cr.ShowPrefixMessage("No binding for C-a g")
	cr.ClearPrefixMessage()
	cr.ClearPrefixMessage()

	want := []string{proto.UIEventPrefixMessageShown, proto.UIEventPrefixMessageHidden}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events[%d] = %q, want %q", i, events[i], want[i])
		}
	}
}

func TestHandlePaneOutputPreservesPrefixMessage(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	var events []string
	cr.OnUIEvent = func(name string) {
		events = append(events, name)
	}

	cr.ShowPrefixMessage("No binding for C-a f")
	cr.HandlePaneOutput(1, []byte("shell output"))
	cr.RenderDiff()

	if !strings.Contains(cr.CaptureDisplay(), "No binding for C-a f") {
		t.Fatalf("pane output should not clear prefix message, got:\n%s", cr.CaptureDisplay())
	}

	want := []string{proto.UIEventPrefixMessageShown}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events[%d] = %q, want %q", i, events[i], want[i])
		}
	}
}

func TestClearPrefixMessageClearsDisplay(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.ShowPrefixMessage("No binding for C-a f")
	cr.RenderDiff()

	if !cr.ClearPrefixMessage() {
		t.Fatal("ClearPrefixMessage should report a state change")
	}
	cr.RenderDiff()

	display := cr.CaptureDisplay()
	if strings.Contains(display, "No binding for C-a f") {
		t.Fatalf("display capture should clear prefix message, got:\n%s", display)
	}
	if cr.ClearPrefixMessage() {
		t.Fatal("second ClearPrefixMessage should report no change")
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

func TestStructuralHandleLayoutClearsPrefixMessageEmitsHidden(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	var events []string
	cr.OnUIEvent = func(name string) {
		events = append(events, name)
	}
	cr.ShowPrefixMessage("No binding for C-a f")

	cr.HandleLayout(threePane80x23())

	want := []string{proto.UIEventPrefixMessageShown, proto.UIEventPrefixMessageHidden}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events[%d] = %q, want %q", i, events[i], want[i])
		}
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
	if resp.CmdErr == "" || !strings.Contains(resp.CmdErr, `pane "nope" not found`) {
		t.Fatalf("CmdErr = %q, want pane not found", resp.CmdErr)
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

	dup := NewClientRenderer(80, 24)
	dup.HandleLayout(duplicateNameTwoPane80x23("shared"))
	resp = dup.HandleCaptureRequest([]string{"shared"}, nil)
	if resp.CmdErr == "" || !strings.Contains(resp.CmdErr, `pane "shared" is ambiguous`) {
		t.Fatalf("ambiguous pane capture CmdErr = %q, want ambiguous", resp.CmdErr)
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

func TestClientRendererCopyModeRespectsScrollbackLimitAfterHydration(t *testing.T) {
	t.Parallel()

	cr := NewClientRendererWithScrollback(20, 4, 2)
	cr.HandleLayout(singlePane20x3())
	cr.HandlePaneHistory(1, []string{"old-1", "old-2", "old-3"})
	cr.HandlePaneOutput(1, []byte("cur-1\r\ncur-2\r\ncur-3\r\ncur-4"))

	cr.EnterCopyMode(1)
	cm := cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("copy mode should exist for pane-1")
	}
	if got := cm.TotalLines(); got != 4 {
		t.Fatalf("TotalLines() = %d, want 4", got)
	}

	want := []string{"cur-1", "cur-2", "cur-3", "cur-4"}
	for i, wantLine := range want {
		if got := cm.LineText(i); got != wantLine {
			t.Fatalf("LineText(%d) = %q, want %q", i, got, wantLine)
		}
	}
}

func TestClientRendererCopyModeSnapshotFreezesUntilReenter(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(20, 4)
	cr.HandleLayout(singlePane20x3())
	cr.HandlePaneOutput(1, []byte("old-1\r\nold-2"))

	cr.EnterCopyMode(1)
	cm := cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("copy mode should exist for pane-1")
	}
	before := cm.TotalLines()

	cr.HandlePaneOutput(1, []byte("\r\nnew-after-enter"))

	cm = cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("copy mode should still exist for pane-1")
	}
	if got := cm.TotalLines(); got != before {
		t.Fatalf("TotalLines() after live pane output = %d, want frozen %d", got, before)
	}
	lastBefore := cm.LineText(before - 1)
	if strings.Contains(lastBefore, "new-after-enter") {
		t.Fatalf("copy mode line = %q, want frozen snapshot without new output", lastBefore)
	}

	cr.ExitCopyMode(1)
	cr.EnterCopyMode(1)
	cm = cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("copy mode should re-enter for pane-1")
	}
	found := false
	for i := 0; i < cm.TotalLines(); i++ {
		if strings.Contains(cm.LineText(i), "new-after-enter") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("re-entered copy mode should include new pane output")
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

func TestRescaleLayoutForSmallerClientResizesEmulators(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(40, 12)
	cr.HandleLayout(twoPane80x23())

	w, h, ok := cr.renderer.PaneSize(1)
	if !ok {
		t.Fatal("pane-1 emulator missing")
	}
	if w != 19 || h != 10 {
		t.Fatalf("pane-1 emulator size on smaller client = %dx%d, want 19x10", w, h)
	}

	const wideLine = "1234567890123456789012345678901234567890"
	cr.HandlePaneOutput(1, []byte("\033[2J\033[H"+wideLine))

	var pane proto.CapturePane
	if err := json.Unmarshal([]byte(cr.CapturePaneJSON(1, nil)), &pane); err != nil {
		t.Fatalf("CapturePaneJSON parse: %v", err)
	}
	wantLines := []string{wideLine[:19], wideLine[19:38], wideLine[38:]}
	if len(pane.Content) < len(wantLines) {
		t.Fatalf("pane content lines = %d, want at least %d", len(pane.Content), len(wantLines))
	}
	for i, want := range wantLines {
		if got := pane.Content[i]; got != want {
			t.Fatalf("pane line %d = %q, want %q", i, got, want)
		}
	}
	if got := pane.Cursor; got.Col != 2 || got.Row != 2 {
		t.Fatalf("pane cursor = (%d,%d), want (2,2)", got.Col, got.Row)
	}
}

func TestClientRendererResizeRescalesLayoutImmediately(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(20, 6)
	cr.HandleLayout(singlePane20x5())

	cr.Resize(20, 4)

	layout := cr.VisibleLayout()
	if layout == nil {
		t.Fatal("VisibleLayout() = nil, want single pane layout")
	}
	if layout.H != 3 {
		t.Fatalf("layout height after resize = %d, want 3", layout.H)
	}

	text := cr.Capture(true)
	lines := strings.Split(text, "\n")
	if len(lines) != 4 {
		t.Fatalf("capture lines after resize = %d, want 4", len(lines))
	}
}

func TestClientRendererResizeResizesEmulatorImmediately(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(20, 6)
	cr.HandleLayout(singlePane20x5())

	cr.Resize(20, 4)

	w, h, ok := cr.renderer.PaneSize(1)
	if !ok {
		t.Fatal("Emulator(1) = missing")
	}
	if w != 20 || h != 2 {
		t.Fatalf("emulator size after resize = %dx%d, want 20x2", w, h)
	}
}

func TestRescaleLayoutForSmallerClientClampsOversizedScrollRegion(t *testing.T) {
	t.Parallel()

	// The larger client owns the server PTY size, but this client still replays
	// the pane into its smaller local emulator.
	cr := NewClientRenderer(40, 12)
	cr.HandleLayout(twoPane80x23())

	w, h, ok := cr.renderer.PaneSize(1)
	if !ok {
		t.Fatal("pane-1 emulator missing")
	}
	if w != 19 || h != 10 {
		t.Fatalf("pane-1 emulator size on smaller client = %dx%d, want 19x10", w, h)
	}

	cr.HandlePaneOutput(1, []byte("\x1b[1;23r\x1b[H\x1bMok"))

	if got := cr.CapturePaneText(1, false); !strings.Contains(got, "ok") {
		t.Fatalf("CapturePaneText() = %q, want substring %q", got, "ok")
	}
	if got := cr.RenderDiff(); got == "" {
		t.Fatal("RenderDiff after oversized scroll region should produce output")
	}
}
