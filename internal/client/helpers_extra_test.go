package client

import (
	"bytes"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/weill-labs/amux/internal/proto"
)

func TestAdvertisedAttachCapabilitiesUsesProcessEnv(t *testing.T) {
	t.Setenv("TERM", "xterm-kitty")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("KITTY_WINDOW_ID", "42")
	t.Setenv("AMUX_CLIENT_CAPABILITIES", "-graphics_placeholder,prompt_markers")

	got := advertisedAttachCapabilities()
	want := &proto.ClientCapabilities{
		KittyKeyboard:       true,
		Hyperlinks:          true,
		RichUnderline:       true,
		PromptMarkers:       true,
		CursorMetadata:      false,
		BinaryPaneHistory:   true,
		PredictionSupported: true,
	}

	if got == nil {
		t.Fatal("advertisedAttachCapabilities returned nil")
	}
	if got.KittyKeyboard != want.KittyKeyboard ||
		got.Hyperlinks != want.Hyperlinks ||
		got.RichUnderline != want.RichUnderline ||
		got.PromptMarkers != want.PromptMarkers ||
		got.BinaryPaneHistory != want.BinaryPaneHistory ||
		got.PredictionSupported != want.PredictionSupported ||
		got.GraphicsPlaceholder != want.GraphicsPlaceholder {
		t.Fatalf("advertisedAttachCapabilities() = %+v, want %+v", *got, *want)
	}
}

func TestRendererOverlayStateCommandFeedbackAndActiveCopyMode(t *testing.T) {
	t.Parallel()

	cr := buildMultiWindowRenderer(t)
	if !cr.ShowDisplayPanes() {
		t.Fatal("ShowDisplayPanes should succeed")
	}

	overlay := cr.overlayState()
	if len(overlay.PaneLabels) == 0 {
		t.Fatal("overlayState should include pane labels")
	}
	if overlay.Chooser != nil || overlay.Message != "" {
		t.Fatalf("overlayState = %+v, want labels only", overlay)
	}

	cr.ShowPrefixMessage("prefix message")
	overlay = cr.overlayState()
	if overlay.Message != "prefix message" {
		t.Fatalf("overlay message = %q, want prefix message", overlay.Message)
	}
	if !cr.ClearCommandFeedback() {
		t.Fatal("ClearCommandFeedback should report a change")
	}
	if cr.ClearCommandFeedback() {
		t.Fatal("second ClearCommandFeedback should be a no-op")
	}

	activePaneID := cr.ActivePaneID()
	cr.EnterCopyMode(activePaneID)
	if cr.ActiveCopyMode() == nil {
		t.Fatal("ActiveCopyMode should return the active pane's copy mode")
	}
	cr.ExitCopyMode(activePaneID)
	if cr.ActiveCopyMode() != nil {
		t.Fatal("ActiveCopyMode should be nil after ExitCopyMode")
	}
}

func TestChooserHelpersAndInputBranches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mode       chooserMode
		wantTitle  string
		wantShown  string
		wantHidden string
	}{
		{mode: chooserModeTree, wantTitle: "choose-tree", wantShown: proto.UIEventChooseTreeShown, wantHidden: proto.UIEventChooseTreeHidden},
		{mode: chooserModeWindow, wantTitle: "choose-window", wantShown: proto.UIEventChooseWindowShown, wantHidden: proto.UIEventChooseWindowHidden},
		{mode: chooserMode("custom"), wantTitle: "chooser"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(string(tt.mode), func(t *testing.T) {
			t.Parallel()
			if got := tt.mode.title(); got != tt.wantTitle {
				t.Fatalf("title() = %q, want %q", got, tt.wantTitle)
			}
			if got := tt.mode.shownEvent(); got != tt.wantShown {
				t.Fatalf("shownEvent() = %q, want %q", got, tt.wantShown)
			}
			if got := tt.mode.hiddenEvent(); got != tt.wantHidden {
				t.Fatalf("hiddenEvent() = %q, want %q", got, tt.wantHidden)
			}
		})
	}

	cr := buildMultiWindowRenderer(t)
	if cr.ChooserActive() {
		t.Fatal("ChooserActive should be false before ShowChooser")
	}
	if !cr.ShowChooser(chooserModeTree) {
		t.Fatal("ShowChooser tree should succeed")
	}
	if !cr.ChooserActive() {
		t.Fatal("ChooserActive should be true after ShowChooser")
	}

	overlay := cr.chooserOverlay()
	if overlay == nil || overlay.Title != "choose-tree" {
		t.Fatalf("chooserOverlay = %+v, want choose-tree overlay", overlay)
	}

	if cmd := cr.HandleChooserInput([]byte{0x1b, '[', 'B'}); cmd.bell || cmd.command != "" {
		t.Fatalf("down-arrow command = %+v, want movement only", cmd)
	}
	if cmd := cr.HandleChooserInput([]byte{0x03}); !cmd.bell {
		t.Fatalf("invalid control byte should ring bell, got %+v", cmd)
	}

	cr.HandleChooserInput([]byte("gpu"))
	if got := cr.chooserOverlay().Query; got != "gpu" {
		t.Fatalf("query after printable input = %q, want gpu", got)
	}
	cr.HandleChooserInput([]byte("q"))
	if got := cr.chooserOverlay().Query; got != "gpuq" {
		t.Fatalf("query after q with non-empty query = %q, want gpuq", got)
	}
	cr.HandleChooserInput([]byte{0x7f})
	if got := cr.chooserOverlay().Query; got != "gpu" {
		t.Fatalf("query after backspace = %q, want gpu", got)
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
		t.Fatal("q should hide chooser when the query is empty")
	}
}

func TestMoveAndSelectChooserBellPaths(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(20, 8)
	cr.updateState(func(next *clientSnapshot) clientUIResult {
		next.ui.chooser = &chooserState{
			mode:  chooserModeWindow,
			items: []chooserItem{{text: "header", selectable: false}},
		}
		return clientUIResult{}
	})

	if got := cr.moveChooser(1); !got.bell {
		t.Fatalf("moveChooser should bell when no rows are selectable, got %+v", got)
	}
	if got := cr.selectChooser(); !got.bell {
		t.Fatalf("selectChooser should bell when selected row is not selectable, got %+v", got)
	}
}

func TestChooserNavigationCoverageHelpers(t *testing.T) {
	t.Parallel()

	if got := chooserListHeight(0); got != 1 {
		t.Fatalf("chooserListHeight(0) = %d, want 1", got)
	}

	delegate := chooserListDelegate{}
	var buf bytes.Buffer
	model := list.New(nil, delegate, 10, 1)
	delegate.Render(&buf, model, 0, chooserListItem{})
	if got := delegate.Height(); got != 1 {
		t.Fatalf("delegate.Height() = %d, want 1", got)
	}
	if got := delegate.Spacing(); got != 0 {
		t.Fatalf("delegate.Spacing() = %d, want 0", got)
	}
	if cmd := delegate.Update(tea.WindowSizeMsg{}, &model); cmd != nil {
		t.Fatalf("delegate.Update() = %v, want nil", cmd)
	}

	cr := buildMultiWindowRenderer(t)
	if got := cr.chooserQueryValue(); got != "" {
		t.Fatalf("chooserQueryValue() without chooser = %q, want empty", got)
	}

	if !cr.ShowChooser(chooserModeWindow) {
		t.Fatal("ShowChooser window should succeed")
	}
	if got := cr.HandleChooserInput([]byte("k")); got.bell {
		t.Fatalf("k should wrap to the last row, got %+v", got)
	}
	if cmd := cr.selectChooser(); cmd.command != "select-window" || len(cmd.args) != 1 || cmd.args[0] != "2" {
		t.Fatalf("selection after k wrap = %+v, want window 2", cmd)
	}

	if !cr.ShowChooser(chooserModeWindow) {
		t.Fatal("ShowChooser window should succeed after wrap")
	}
	if got := cr.HandleChooserInput([]byte("\x1b[A")); got.bell {
		t.Fatalf("up arrow should navigate the chooser, got %+v", got)
	}
	if cmd := cr.selectChooser(); cmd.command != "select-window" || len(cmd.args) != 1 || cmd.args[0] != "2" {
		t.Fatalf("selection after up arrow = %+v, want window 2", cmd)
	}

	if !cr.ShowChooser(chooserModeWindow) {
		t.Fatal("ShowChooser window should succeed for page navigation")
	}
	if got := cr.HandleChooserInput([]byte("\x1b[5~")); got.bell {
		t.Fatalf("page up should be accepted, got %+v", got)
	}
	if got := cr.HandleChooserInput([]byte("\x1b[6~")); got.bell {
		t.Fatalf("page down should be accepted, got %+v", got)
	}

	cr.HandleChooserInput([]byte("no-match"))
	if got := cr.HandleChooserInput([]byte("\x1b[6~")); !got.bell {
		t.Fatalf("page down without visible rows should bell, got %+v", got)
	}
}
