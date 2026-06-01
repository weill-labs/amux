package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
)

const windowSelectorGoldenWidth = 78

func TestGoldenRemoteMirrorWindowSelector(t *testing.T) {
	t.Parallel()

	grid := NewScreenGrid(windowSelectorGoldenWidth, 1)
	buildGlobalBarCells(grid, "session", 4, windowSelectorGoldenWidth, 0, []WindowInfo{
		{Index: 1, Name: "local", IsActive: true},
		{Index: 2, Name: "hetzner-1:main", IsRemoteMirror: true},
		{Index: 3, Name: "logs"},
	}, "", time.Date(2025, 1, 1, 12, 34, 0, 0, time.UTC))

	assertWindowSelectorGolden(t, "remote_mirror_window_selector.golden", strings.TrimRight(gridRowText(grid, 0, windowSelectorGoldenWidth), " ")+"\n")
	assertWindowSelectorGolden(t, "remote_mirror_window_selector.color", windowSelectorColorMap(grid, 0,
		"1:local",
		"2:hetzner-1:main",
		"3:logs",
	)+"\n")
}

func assertWindowSelectorGolden(t *testing.T, name, actual string) {
	t.Helper()

	path := filepath.Join("testdata", name)
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v\nActual:\n%s", path, err, actual)
	}
	if actual != string(expected) {
		t.Fatalf("golden mismatch: %s\n\n--- expected ---\n%s\n--- actual ---\n%s", path, expected, actual)
	}
}

func windowSelectorColorMap(grid *ScreenGrid, y int, labels ...string) string {
	row := strings.Repeat(" ", grid.Width)
	rowBytes := []byte(row)
	for _, label := range labels {
		start := findRowLabel(grid, y, grid.Width, label)
		if start < 0 {
			continue
		}
		for offset := 0; offset < len(label); offset++ {
			rowBytes[start+offset] = windowSelectorColorLetter(grid.Get(start+offset, y))
		}
	}
	return strings.TrimRight(string(rowBytes), " ")
}

func windowSelectorColorLetter(cell ScreenCell) byte {
	switch {
	case cellColorMatches(cell, config.BlueHex):
		return 'B'
	case cellColorMatches(cell, config.GreenHex):
		return 'G'
	case cellColorMatches(cell, config.TextColorHex):
		return '|'
	default:
		return '?'
	}
}

func cellColorMatches(cell ScreenCell, wantHex string) bool {
	if cell.Style.Fg == nil {
		return false
	}
	got, ok := cell.Style.Fg.(interface {
		RGBA() (uint32, uint32, uint32, uint32)
	})
	want := hexToColor(wantHex)
	return ok && sameColor(got, want)
}
