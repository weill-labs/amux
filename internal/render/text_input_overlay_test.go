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

func TestTextInputOverlayGuardsAndClamping(t *testing.T) {
	t.Parallel()

	t.Run("nil overlay renders nothing", func(t *testing.T) {
		t.Parallel()

		grid := NewScreenGrid(20, 8)
		buildTextInputOverlayCells(grid, nil)

		var buf strings.Builder
		renderTextInputOverlay(&buf, 20, 8, nil)
		if got := buf.String(); got != "" {
			t.Fatalf("renderTextInputOverlay(nil) = %q, want empty", got)
		}
	})

	t.Run("layout guards", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			width   int
			height  int
			overlay *TextInputOverlay
		}{
			{name: "nil overlay", width: 20, height: 8},
			{name: "non-positive width", width: 0, height: 8, overlay: &TextInputOverlay{Title: "rename-window"}},
			{name: "tiny width", width: 8, height: 8, overlay: &TextInputOverlay{Title: "rename-window"}},
			{name: "tiny height", width: 20, height: 2, overlay: &TextInputOverlay{Title: "rename-window"}},
		}

		for _, tt := range tests {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				lines, x, y := textInputOverlayLayout(tt.width, tt.height, tt.overlay)
				if lines != nil || x != 0 || y != 0 {
					t.Fatalf("textInputOverlayLayout(%d, %d, %+v) = (%v, %d, %d), want nil layout", tt.width, tt.height, tt.overlay, lines, x, y)
				}
			})
		}
	})

	t.Run("input width can drive modal width", func(t *testing.T) {
		t.Parallel()

		overlay := &TextInputOverlay{
			Title: "rename-window",
			Input: "this-is-a-very-long-window-name",
		}
		lines, x, y := textInputOverlayLayout(18, 8, overlay)
		if len(lines) != 3 {
			t.Fatalf("len(lines) = %d, want 3", len(lines))
		}
		if x < 0 || y < 0 {
			t.Fatalf("layout origin = (%d,%d), want non-negative", x, y)
		}
		for _, line := range lines {
			if len(line) > 14 {
				t.Fatalf("line %q exceeds clamped max width", line)
			}
		}
	})
}
