package client

import (
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/config"
)

func TestBuildHelpOverlayUsesBindingMap(t *testing.T) {
	t.Parallel()

	kb := config.DefaultKeybindings()
	delete(kb.Bindings, 'q')
	kb.Bindings['g'] = config.Binding{Action: "display-panes"}

	overlay := buildHelpOverlay(kb)
	if overlay == nil {
		t.Fatal("buildHelpOverlay returned nil")
	}
	if overlay.title() != helpOverlayTitle {
		t.Fatalf("overlay.title() = %q, want %q", overlay.title(), helpOverlayTitle)
	}
	if !strings.Contains(overlay.query, "Ctrl-a") {
		t.Fatalf("overlay.query = %q, want Ctrl-a prefix hint", overlay.query)
	}
	rows := helpOverlayRowText(overlay)
	if !containsHelpRow(rows, "  h/j/k/l, arrows, o, g") {
		t.Fatalf("rows = %v, want navigation row with remapped display-panes key", rows)
	}
	if containsHelpRow(rows, "  h/j/k/l, arrows, o, q") {
		t.Fatalf("rows = %v, want help overlay to avoid hardcoded q binding", rows)
	}
}

func TestHelpOverlayDisplayOnlyAndDismiss(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	if !cr.ShowHelpOverlay(config.DefaultKeybindings()) {
		t.Fatal("ShowHelpOverlay should succeed")
	}
	if !cr.HelpOverlayActive() {
		t.Fatal("HelpOverlayActive should be true after ShowHelpOverlay")
	}

	overlay := cr.helpOverlay()
	if overlay == nil {
		t.Fatal("helpOverlay should be active")
	}
	rows := helpOverlayRowText(overlay)
	for _, want := range []string{
		"Navigation",
		"  h/j/k/l, arrows, o, q",
		"Layout",
		"  \\, -, |, _, a, =",
		"Pane ops",
		"  x, z, U, {, }",
		"Windows",
		"  c, n/p, 1-9, ,, ;",
		"Other",
		"  [, r, d, P, s, w",
	} {
		if !containsHelpRow(rows, want) {
			t.Fatalf("rows = %v, want %q", rows, want)
		}
	}

	cr.RenderDiff()

	display := cr.CaptureDisplay()
	if !strings.Contains(display, helpOverlayTitle) || !strings.Contains(display, "Navigation") {
		t.Fatalf("display capture should include help overlay, got:\n%s", display)
	}

	plain := cr.Capture(true)
	if strings.Contains(plain, helpOverlayTitle) || strings.Contains(plain, "Navigation") {
		t.Fatalf("plain capture should not include help overlay, got:\n%s", plain)
	}

	if !cr.HideHelpOverlay() {
		t.Fatal("HideHelpOverlay should report a state change")
	}
	if cr.HelpOverlayActive() {
		t.Fatal("HelpOverlayActive should be false after HideHelpOverlay")
	}
}

func TestHelpOverlayConsumedEvents(t *testing.T) {
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
			if got := helpOverlayConsumedEvents(decodeInputEvents(tt.input), kb); got != tt.want {
				t.Fatalf("helpOverlayConsumedEvents(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func helpOverlayRowText(overlay *helpOverlayState) []string {
	if overlay == nil {
		return nil
	}
	rows := make([]string, len(overlay.rows))
	for i, row := range overlay.rows {
		rows[i] = row.Text
	}
	return rows
}

func containsHelpRow(rows []string, want string) bool {
	for _, row := range rows {
		if row == want {
			return true
		}
	}
	return false
}
