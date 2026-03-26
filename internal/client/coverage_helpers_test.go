package client

import (
	"bytes"
	"net"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weill-labs/amux/internal/copymode"
	"github.com/weill-labs/amux/internal/mouse"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

func horizontalSnapshot() *proto.LayoutSnapshot {
	root := proto.CellSnapshot{
		X: 0, Y: 0, W: 80, H: 23,
		Dir: int(mux.SplitHorizontal),
		Children: []proto.CellSnapshot{
			{X: 0, Y: 0, W: 80, H: 11, IsLeaf: true, Dir: -1, PaneID: 1},
			{X: 0, Y: 12, W: 80, H: 11, IsLeaf: true, Dir: -1, PaneID: 2},
		},
	}
	panes := []proto.PaneSnapshot{
		{ID: 1, Name: "pane-1", Host: "local", Color: "f5e0dc"},
		{ID: 2, Name: "pane-2", Host: "local", Color: "f2cdcd"},
	}
	return &proto.LayoutSnapshot{
		SessionName:  "test",
		ActivePaneID: 2,
		Width:        80,
		Height:       23,
		Root:         root,
		Panes:        panes,
	}
}

func TestOverlayStateCloneAndLabels(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.ShowPrefixMessage("prefix")
	if !cr.ShowDisplayPanes() {
		t.Fatal("ShowDisplayPanes should succeed")
	}

	overlay := cr.overlayState()
	if overlay.Message != "prefix" {
		t.Fatalf("overlay message = %q, want %q", overlay.Message, "prefix")
	}
	if len(overlay.PaneLabels) != 2 {
		t.Fatalf("len(PaneLabels) = %d, want 2", len(overlay.PaneLabels))
	}

	labels := cr.overlayLabels()
	labels[0].Label = "x"
	if got := cr.overlayLabels()[0].Label; got == "x" {
		t.Fatal("overlayLabels should return a copy")
	}

	cloned := cloneDisplayPanesState(&displayPanesState{
		labels:  []render.PaneOverlayLabel{{PaneID: 1, Label: "1"}},
		targets: map[byte]uint32{'1': 1},
	})
	cloned.labels[0].Label = "2"
	cloned.targets['1'] = 9
	if got := cr.loadState().ui.displayPanes.labels[0].Label; got != "1" {
		t.Fatalf("display panes label = %q, want %q", got, "1")
	}
	if got := cr.loadState().ui.displayPanes.targets['1']; got != 1 {
		t.Fatalf("display panes target = %d, want 1", got)
	}
}

func TestHistoryEmulatorSizeWheelScrollAndSubtreeVisibility(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(20, 4)
	cr.HandleLayout(singlePane20x3())
	cr.HandlePaneHistory(1, []string{"older-1", "older-2"})
	cr.HandlePaneOutput(1, []byte("screen"))

	emu, ok := cr.Emulator(1)
	if !ok {
		t.Fatal("Emulator(1) = missing")
	}
	h := &historyEmulator{emu: emu, baseHistory: []string{"older-1"}, scrollbackLines: 4}
	if w, hgt := h.Size(); w != 20 || hgt != 2 {
		t.Fatalf("historyEmulator.Size() = %dx%d, want 20x2", w, hgt)
	}

	if got := cr.WheelScrollCopyMode(1, 1, true); got != copymode.ActionNone {
		t.Fatalf("WheelScrollCopyMode without copy mode = %d, want ActionNone", got)
	}

	cr.EnterCopyMode(1)
	if cr.ActiveCopyMode() == nil {
		t.Fatal("ActiveCopyMode() = nil, want copy mode")
	}
	if got := cr.WheelScrollCopyMode(1, 1, true); got != copymode.ActionRedraw {
		t.Fatalf("WheelScrollCopyMode up = %d, want ActionRedraw", got)
	}
	if !cr.IsDirty() {
		t.Fatal("WheelScrollCopyMode up should mark the renderer dirty")
	}

	cm := cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("CopyModeForPane(1) = nil")
	}
	cm.SetScrollExit(true)
	if got := cr.WheelScrollCopyMode(1, 100, false); got != copymode.ActionExit {
		t.Fatalf("WheelScrollCopyMode down = %d, want ActionExit", got)
	}
	if cr.InCopyMode(1) {
		t.Fatal("WheelScrollCopyMode exit should leave copy mode")
	}

	cr = NewClientRenderer(80, 24)
	cr.HandleLayout(horizontalSnapshot())
	layout := cr.Layout()
	if layout == nil || len(layout.Children) != 2 {
		t.Fatal("expected a two-pane horizontal layout")
	}
}

func TestForwardMouseToPaneAndWritePaneInput(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.HandlePaneOutput(1, []byte("\x1b[?1002h\x1b[?1006h"))

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	writePaneInput(sender, 1, nil)
	assertNoMessage(t, serverConn)

	target := &paneMouseTarget{paneID: 1, localX: 2, localY: 1, inContent: true}
	done := make(chan bool, 1)
	go func() {
		done <- forwardMouseToPane(cr, sender, target, mouse.Event{Action: mouse.Press, Button: mouse.ButtonLeft, X: 2, Y: 2})
	}()

	msg := readCommandMessage(t, serverConn)
	if msg.Type != proto.MsgTypeInputPane {
		t.Fatalf("message type = %d, want %d", msg.Type, proto.MsgTypeInputPane)
	}
	if msg.PaneID != 1 || len(msg.PaneData) == 0 {
		t.Fatalf("pane input message = %+v, want pane 1 with encoded data", msg)
	}
	if !<-done {
		t.Fatal("forwardMouseToPane should encode mouse input for panes with mouse protocol")
	}
}

func TestLegacyKeyHelpersAndClientPaneDataAccessors(t *testing.T) {
	t.Parallel()

	ctrlTests := []struct {
		code rune
		want byte
		ok   bool
	}{
		{code: 'a', want: 0x01, ok: true},
		{code: 'Z', want: 0x1a, ok: true},
		{code: '@', want: 0x00, ok: true},
		{code: uv.KeyEscape, want: 0x1b, ok: true},
		{code: '\\', want: 0x1c, ok: true},
		{code: ']', want: 0x1d, ok: true},
		{code: '^', want: 0x1e, ok: true},
		{code: '_', want: 0x1f, ok: true},
		{code: '?', want: 0x7f, ok: true},
		{code: uv.KeyTab, want: '\t', ok: true},
		{code: uv.KeyEnter, want: '\r', ok: true},
		{code: uv.KeyBackspace, want: 0x08, ok: true},
		{code: '1', want: '1', ok: true},
		{code: '0', want: '0', ok: true},
		{code: '=', want: '=', ok: true},
		{code: ';', want: ';', ok: true},
		{code: '\'', want: '\'', ok: true},
		{code: ',', want: ',', ok: true},
		{code: '.', want: '.', ok: true},
		{code: '8', want: 0x7f, ok: true},
		{code: '2', want: 0x00, ok: true},
		{code: '3', want: 0x1b, ok: true},
		{code: '9', want: '9', ok: true},
		{code: '/', want: 0x1f, ok: true},
	}
	for _, tt := range ctrlTests {
		got, ok := legacyCtrlByte(tt.code)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("legacyCtrlByte(%q) = (%d, %v), want (%d, %v)", tt.code, got, ok, tt.want, tt.ok)
		}
	}

	specialTests := []struct {
		code rune
		want []byte
	}{
		{code: uv.KeyEnter, want: []byte{'\r'}},
		{code: uv.KeyTab, want: []byte{'\t'}},
		{code: uv.KeyEscape, want: []byte{0x1b}},
		{code: uv.KeyBackspace, want: []byte{0x7f}},
		{code: uv.KeyUp, want: []byte{0x1b, '[', 'A'}},
		{code: uv.KeyDown, want: []byte{0x1b, '[', 'B'}},
		{code: uv.KeyRight, want: []byte{0x1b, '[', 'C'}},
		{code: uv.KeyLeft, want: []byte{0x1b, '[', 'D'}},
		{code: uv.KeyHome, want: []byte{0x1b, '[', 'H'}},
		{code: uv.KeyEnd, want: []byte{0x1b, '[', 'F'}},
		{code: uv.KeyPgUp, want: []byte{0x1b, '[', '5', '~'}},
		{code: uv.KeyPgDown, want: []byte{0x1b, '[', '6', '~'}},
		{code: uv.KeyDelete, want: []byte{0x1b, '[', '3', '~'}},
		{code: uv.KeyInsert, want: []byte{0x1b, '[', '2', '~'}},
	}
	for _, tt := range specialTests {
		if got := legacySpecialKeySequence(tt.code); !bytes.Equal(got, tt.want) {
			t.Fatalf("legacySpecialKeySequence(%q) = %v, want %v", tt.code, got, tt.want)
		}
	}
	if got := legacySpecialKeySequence('x'); got != nil {
		t.Fatalf("legacySpecialKeySequence('x') = %v, want nil", got)
	}

	if got, ok := asciiRuneByte('x'); !ok || got != 'x' {
		t.Fatalf("asciiRuneByte('x') = (%d, %v), want ('x', true)", got, ok)
	}
	if got, ok := asciiRuneByte('界'); ok || got != 0 {
		t.Fatalf("asciiRuneByte('界') = (%d, %v), want (0, false)", got, ok)
	}
	if got := legacyCtrlRune(uv.Key{Text: "x"}); got != 'x' {
		t.Fatalf("legacyCtrlRune(text) = %q, want 'x'", got)
	}
	if got := legacyCtrlRune(uv.Key{Code: 'a', ShiftedCode: 'A', Mod: uv.ModAlt | uv.ModShift}); got != 'A' {
		t.Fatalf("legacyCtrlRune(shifted alt key) = %q, want 'A'", got)
	}

	cr := NewClientRenderer(20, 4)
	cr.HandleLayout(singlePane20x3())
	cr.HandlePaneHistory(1, []string{"search target"})
	cr.EnterCopyMode(1)

	emu, ok := cr.Emulator(1)
	if !ok {
		t.Fatal("Emulator(1) = missing")
	}
	cm := cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("CopyModeForPane(1) = nil")
	}
	_ = cm.HandleInput([]byte{'/', 'o', 'l', 'd'})

	pd := &clientPaneData{
		emu: emu,
		info: proto.PaneSnapshot{
			ID:         1,
			Name:       "pane-1",
			Host:       "local",
			Task:       "task",
			Color:      "f5e0dc",
			ConnStatus: "connected",
		},
		cm: cm,
	}
	if pd.ID() != 1 {
		t.Fatalf("ID() = %d, want 1", pd.ID())
	}
	if got := pd.CopyModeSearch(); got != "/old" {
		t.Fatalf("CopyModeSearch() = %q, want %q", got, "/old")
	}
	if _, row := pd.CursorPos(); row < 0 {
		t.Fatalf("CursorPos row = %d, want non-negative", row)
	}
	if !pd.CursorHidden() {
		t.Fatal("CursorHidden() in copy mode = false, want true")
	}
	if pd.HasCursorBlock() {
		t.Fatal("HasCursorBlock() in copy mode = true, want false")
	}
}

func TestHandleChooserInputBranches(t *testing.T) {
	t.Parallel()

	cr := buildMultiWindowRenderer(t)
	if got := cr.HandleChooserInput(nil); got.command != "" || got.bell || len(got.args) != 0 {
		t.Fatalf("HandleChooserInput(nil) = %+v, want zero command", got)
	}
	if got := cr.HandleChooserInput([]byte("x")); got.command != "" || got.bell || len(got.args) != 0 {
		t.Fatalf("HandleChooserInput without chooser = %+v, want zero command", got)
	}

	if !cr.ShowChooser(chooserModeWindow) {
		t.Fatal("ShowChooser window should succeed")
	}
	cmd := cr.HandleChooserInput([]byte("\x1b[B"))
	if cmd.command != "" || cmd.bell || len(cmd.args) != 0 {
		t.Fatalf("down-arrow chooser command = %+v, want zero command", cmd)
	}
	cmd = cr.HandleChooserInput([]byte{'\r'})
	if cmd.command != "select-window" || len(cmd.args) != 1 || cmd.args[0] != "2" {
		t.Fatalf("enter chooser command = %+v, want select-window 2", cmd)
	}
	if cr.ChooserActive() {
		t.Fatal("chooser should hide after selection")
	}

	if !cr.ShowChooser(chooserModeTree) {
		t.Fatal("ShowChooser tree should succeed")
	}
	cmd = cr.HandleChooserInput([]byte{0x01})
	if !cmd.bell {
		t.Fatalf("control byte chooser command = %+v, want bell", cmd)
	}
	cr.HandleChooserInput([]byte("gpu"))
	if overlay := cr.chooserOverlay(); overlay == nil || overlay.Query != "gpu" {
		t.Fatalf("chooser overlay = %+v, want query %q", overlay, "gpu")
	}
	cr.HandleChooserInput([]byte{0x7f})
	if overlay := cr.chooserOverlay(); overlay == nil || overlay.Query != "gp" {
		t.Fatalf("chooser overlay after backspace = %+v, want query %q", overlay, "gp")
	}
	cr.HandleChooserInput([]byte{0x1b})
	if cr.ChooserActive() {
		t.Fatal("escape should hide chooser")
	}

	if !cr.ShowChooser(chooserModeWindow) {
		t.Fatal("ShowChooser window should succeed")
	}
	cr.HandleChooserInput([]byte("q"))
	if cr.ChooserActive() {
		t.Fatal("q on empty chooser query should hide chooser")
	}
}

func TestMessageSenderCloseAndSendAfterClose(t *testing.T) {
	t.Parallel()

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	sender.Close()
	sender.Close()
	if err := sender.Send(&proto.Message{Type: proto.MsgTypeDetach}); err != nil {
		t.Fatalf("Send() after Close() = %v, want nil", err)
	}
}
