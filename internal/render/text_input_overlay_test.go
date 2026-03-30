package render

import (
	"strings"
	"testing"
)

func TestTextInputOverlayLayoutAndRendering(t *testing.T) {
	t.Parallel()

	overlay := &TextInputOverlay{
		Title: "rename-window",
		Input: "logs",
	}

	lines, x, y := textInputOverlayLayout(32, 12, overlay)
	if len(lines) == 0 {
		t.Fatal("textInputOverlayLayout returned no lines")
	}
	if x < 0 || y < 0 {
		t.Fatalf("text input overlay origin = (%d,%d), want non-negative", x, y)
	}

	grid := NewScreenGrid(32, 12)
	buildTextInputOverlayCells(grid, overlay)
	text := gridToText(grid)
	for _, line := range lines {
		if !strings.Contains(text, line) {
			t.Fatalf("grid text missing overlay line %q:\n%s", line, text)
		}
	}

	var buf strings.Builder
	renderTextInputOverlay(&buf, 32, 12, overlay)
	rendered := buf.String()
	for row, line := range lines {
		if !strings.Contains(rendered, line) {
			t.Fatalf("rendered overlay missing line %q:\n%s", line, rendered)
		}
		if !strings.Contains(rendered, cursorPos(y+row+1, x+1)) {
			t.Fatalf("rendered overlay missing cursor position for row %d", row)
		}
	}
}
