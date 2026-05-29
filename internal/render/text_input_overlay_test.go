package render

import (
	"strings"
	"testing"
)

func TestTextInputOverlayRendersChrome(t *testing.T) {
	t.Parallel()

	overlay := &TextInputOverlay{Title: "rename-window", Input: "logs"}

	grid := NewScreenGrid(40, 12)
	buildTextInputOverlayCells(grid, overlay)
	text := gridToText(grid)

	for _, want := range []string{"╭", "╮", "╰", "╯", "rename-window", "> logs", "esc cancel"} {
		if !strings.Contains(text, want) {
			t.Errorf("grid text missing %q:\n%s", want, text)
		}
	}

	var buf strings.Builder
	renderTextInputOverlay(&buf, 40, 12, overlay)
	rendered := buf.String()
	// The rendered footer interleaves SGR codes between the bold key and dim
	// label, so check tokens individually rather than as one substring.
	for _, want := range []string{"rename-window", "logs", "enter", "confirm", "esc", "cancel"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered overlay missing %q:\n%s", want, rendered)
		}
	}
}

func TestTextInputOverlayGuards(t *testing.T) {
	t.Parallel()

	t.Run("nil overlay renders nothing", func(t *testing.T) {
		t.Parallel()

		grid := NewScreenGrid(20, 8)
		buildTextInputOverlayCells(grid, nil)
		if got := gridToText(grid); strings.TrimSpace(got) != "" {
			t.Fatalf("buildTextInputOverlayCells(nil) drew %q, want empty", got)
		}

		var buf strings.Builder
		renderTextInputOverlay(&buf, 20, 8, nil)
		if got := buf.String(); got != "" {
			t.Fatalf("renderTextInputOverlay(nil) = %q, want empty", got)
		}
	})

	t.Run("does not fit returns nothing", func(t *testing.T) {
		t.Parallel()

		overlay := &TextInputOverlay{Title: "rename-window", Input: "logs"}
		var buf strings.Builder
		renderTextInputOverlayWithProfile(&buf, 6, 8, overlay, defaultColorProfile)
		if got := buf.String(); got != "" {
			t.Fatalf("renderTextInputOverlay in tiny width = %q, want empty", got)
		}
	})

	t.Run("long input is clamped within screen", func(t *testing.T) {
		t.Parallel()

		overlay := &TextInputOverlay{Title: "rename-window", Input: "this-is-a-very-long-window-name-that-overflows"}
		grid := NewScreenGrid(24, 8)
		buildTextInputOverlayCells(grid, overlay)
		for y := 0; y < grid.Height; y++ {
			for x := 0; x < grid.Width; x++ {
				if grid.Get(x, y).Char == "╮" && x >= grid.Width {
					t.Fatalf("top-right corner at x=%d exceeds screen width %d", x, grid.Width)
				}
			}
		}
	})
}
