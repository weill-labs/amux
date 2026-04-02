package client

import (
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/config"
)

func TestBuildHelpBarUsesBindingMap(t *testing.T) {
	t.Parallel()

	kb := config.DefaultKeybindings()
	delete(kb.Bindings, 'q')
	kb.Bindings['g'] = config.Binding{Action: "display-panes"}

	bar := buildHelpBar(kb)
	if bar == nil {
		t.Fatal("buildHelpBar returned nil")
	}

	view := bar.view(80)
	if !strings.Contains(view, "g panes") {
		t.Fatalf("help bar view = %q, want remapped display-panes binding with a direct description", view)
	}
	if strings.Contains(view, "q panes") {
		t.Fatalf("help bar view = %q, want help bar to avoid hardcoded q binding", view)
	}
	if !strings.Contains(view, "? close") {
		t.Fatalf("help bar view = %q, want the toggle key to advertise close while active", view)
	}
	for _, want := range []string{"\\ root-vsplit", "_ root-hsplit", "x kill", "z zoom"} {
		if !strings.Contains(view, want) {
			t.Fatalf("help bar view = %q, want %q item", view, want)
		}
	}
	for _, forbidden := range []string{" nav", " layout", " pane", " wins", " other"} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("help bar view = %q, should not contain grouped category labels like %q", view, forbidden)
		}
	}

	rows := strings.Split(view, "\n")
	if len(rows) < 2 || len(rows) > 4 {
		t.Fatalf("help bar rows = %d, want 2-4\n%s", len(rows), view)
	}
}

func TestHelpBarReflowsAcrossRows(t *testing.T) {
	t.Parallel()

	bar := buildHelpBar(config.DefaultKeybindings())
	if bar == nil {
		t.Fatal("buildHelpBar returned nil")
	}

	narrow := bar.renderOverlay(80)
	if narrow == nil {
		t.Fatal("renderOverlay(80) returned nil")
	}
	if got := len(narrow.Rows); got < 2 || got > 4 {
		t.Fatalf("renderOverlay(80) rows = %d, want 2-4", got)
	}

	wide := bar.renderOverlay(160)
	if wide == nil {
		t.Fatal("renderOverlay(160) returned nil")
	}
	if len(wide.Rows) >= len(narrow.Rows) {
		t.Fatalf("wide help bar rows = %d, want fewer than narrow rows = %d", len(wide.Rows), len(narrow.Rows))
	}
}

func TestHelpBarDisplayOnlyAndDismiss(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	if !cr.ShowHelpBar(config.DefaultKeybindings()) {
		t.Fatal("ShowHelpBar should succeed")
	}
	if !cr.HelpBarActive() {
		t.Fatal("HelpBarActive should be true after ShowHelpBar")
	}

	cr.RenderDiff()

	display := cr.CaptureDisplay()
	for _, want := range []string{"? close", "x kill", "c new-win"} {
		if !strings.Contains(display, want) {
			t.Fatalf("display capture should include %q in the bottom help bar, got:\n%s", want, display)
		}
	}
	if strings.Contains(display, " nav") || strings.Contains(display, " wins") || strings.Contains(display, " layout") {
		t.Fatalf("display capture should include the bottom help bar, got:\n%s", display)
	}
	if strings.Contains(display, "keybindings") || strings.Contains(display, "Navigation") {
		t.Fatalf("display capture should not include the old modal help overlay, got:\n%s", display)
	}

	plain := cr.Capture(true)
	if strings.Contains(plain, "? help") && strings.Contains(plain, "nav") {
		t.Fatalf("plain capture should not include client-local help bar, got:\n%s", plain)
	}

	if !cr.HideHelpBar() {
		t.Fatal("HideHelpBar should report a state change")
	}
	if cr.HelpBarActive() {
		t.Fatal("HelpBarActive should be false after HideHelpBar")
	}
}

func TestHelpBarConsumedEvents(t *testing.T) {
	t.Parallel()

	kb := config.DefaultKeybindings()

	tests := []struct {
		name  string
		input []byte
		want  int
	}{
		{name: "empty input", want: 0},
		{name: "single dismiss key", input: []byte{'?'}, want: 1},
		{name: "escape dismisses", input: []byte{0x1b}, want: 1},
		{name: "prefix question toggles", input: []byte{kb.Prefix, '?'}, want: 2},
		{name: "prefix other key replays full prefix sequence", input: []byte{kb.Prefix, 'x'}, want: 0},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := helpBarConsumedEvents(decodeInputEvents(tt.input), kb); got != tt.want {
				t.Fatalf("helpBarConsumedEvents(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
