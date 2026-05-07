package mux

import (
	"fmt"
	"strings"
	"testing"
)

func TestVTEmulatorResizeShrinkThenWidenKeepsDenseRowsSeparate(t *testing.T) {
	t.Parallel()

	const (
		width       = 214
		shrinkWidth = 80
		height      = 12
	)
	emu := NewVTEmulatorWithDrain(width, height)
	lines := make([]string, 0, 5)
	for i := 1; i <= 5; i++ {
		line := resizeSmearReproLine(i, width)
		lines = append(lines, line)
		mustWrite(t, emu, []byte(line+"\r\n"))
	}

	before := EmulatorContentLines(emu)
	for i, want := range lines {
		if got := before[i]; got != want {
			t.Fatalf("before resize row %d = %q, want %q", i, got, want)
		}
	}

	emu.Resize(shrinkWidth, height)
	emu.Resize(width, height)

	after := EmulatorContentLines(emu)
	for i := range lines {
		got := after[i]
		marker := fmt.Sprintf("LINE_%d_BEGIN_", i+1)
		if !strings.HasPrefix(got, marker) || strings.Count(got, "LINE_") != 1 {
			t.Fatalf("after shrink/widen row %d = %q, want separate row beginning %q", i, got, marker)
		}
	}
}

func resizeSmearReproLine(i, width int) string {
	letter := string(rune('A' + i - 1))
	prefix := fmt.Sprintf("LINE_%d_BEGIN_", i)
	suffix := fmt.Sprintf("_END_%d", i)
	return prefix + strings.Repeat(letter, width-len(prefix)-len(suffix)) + suffix
}
