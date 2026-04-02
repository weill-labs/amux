package render

import (
	"strings"
	"testing"
)

func TestBuildDropIndicatorCells(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		overlay *DropIndicatorOverlay
		want    []struct {
			x, y int
			char string
		}
	}{
		{
			name:    "rectangle",
			overlay: &DropIndicatorOverlay{X: 1, Y: 2, W: 4, H: 2},
			want: []struct {
				x, y int
				char string
			}{
				{x: 1, y: 2, char: dropPlaceholderChar},
				{x: 2, y: 2, char: dropPlaceholderChar},
				{x: 3, y: 2, char: dropPlaceholderChar},
				{x: 4, y: 2, char: dropPlaceholderChar},
				{x: 1, y: 3, char: dropPlaceholderChar},
				{x: 2, y: 3, char: dropPlaceholderChar},
				{x: 3, y: 3, char: dropPlaceholderChar},
				{x: 4, y: 3, char: dropPlaceholderChar},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			grid := NewScreenGrid(8, 6)
			buildDropIndicatorCells(grid, tt.overlay)

			for _, want := range tt.want {
				if got := grid.Get(want.x, want.y).Char; got != want.char {
					t.Fatalf("grid.Get(%d, %d).Char = %q, want %q", want.x, want.y, got, want.char)
				}
			}
		})
	}
}

func TestRenderDropIndicator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		overlay *DropIndicatorOverlay
		want    string
	}{
		{
			name:    "rectangle",
			overlay: &DropIndicatorOverlay{X: 2, Y: 1, W: 4, H: 2},
			want:    "\x1b[2;3H" + strings.Repeat(dropPlaceholderChar, 4) + "\x1b[3;3H" + strings.Repeat(dropPlaceholderChar, 4),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf strings.Builder
			renderDropIndicator(&buf, tt.overlay)

			if got := buf.String(); !strings.Contains(got, tt.want) {
				t.Fatalf("renderDropIndicator() = %q, want substring %q", got, tt.want)
			}
		})
	}
}
