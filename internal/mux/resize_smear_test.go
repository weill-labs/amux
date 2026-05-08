package mux

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestVTEmulatorResizeShrinkThenWidenKeepsDenseRowsSeparate(t *testing.T) {
	t.Parallel()

	const (
		width       = 214
		shrinkWidth = 80
		height      = 20
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

func TestVTEmulatorResizeShrinkThenWidenPreservesLongShellRows(t *testing.T) {
	t.Parallel()

	const (
		width       = 64
		shrinkWidth = 24
		height      = 8
	)
	emu := NewVTEmulatorWithDrain(width, height)
	line := "nums: 100, 101, 102, 103, 104, 105, 106, 107"
	mustWrite(t, emu, []byte("\x1b[2J\x1b[H"+line+"\r\nPROMPT$ "))

	before := EmulatorContentLines(emu)
	if got := before[0]; got != line {
		t.Fatalf("before resize row 0 = %q, want %q", got, line)
	}

	emu.Resize(shrinkWidth, height)
	afterShrink := EmulatorContentLines(emu)
	if got := strings.Join(afterShrink[:3], ""); !strings.Contains(got, line) {
		t.Fatalf("after shrink rows did not preserve line:\n%q\nwant substring %q", afterShrink[:3], line)
	}

	emu.Resize(width, height)
	afterWiden := EmulatorContentLines(emu)
	if got := afterWiden[0]; got != line {
		t.Fatalf("after widen row 0 = %q, want %q", got, line)
	}
}

func TestVTEmulatorResizeShrinkKeepsHardNewlineAfterFullWidthRow(t *testing.T) {
	t.Parallel()

	const (
		width       = 20
		shrinkWidth = 12
		height      = 6
	)
	emu := NewVTEmulatorWithDrain(width, height)
	fullWidthLine := "ABCDEFGHIJKLMNOPQRST"
	nextLine := "short"
	mustWrite(t, emu, []byte("\x1b[2J\x1b[H"+fullWidthLine+"\r\n"+nextLine))

	emu.Resize(shrinkWidth, height)
	afterShrink := EmulatorContentLines(emu)
	if got, want := afterShrink[0], fullWidthLine[:shrinkWidth]; got != want {
		t.Fatalf("after shrink row 0 = %q, want %q", got, want)
	}
	if got, want := afterShrink[1], fullWidthLine[shrinkWidth:]; got != want {
		t.Fatalf("after shrink row 1 = %q, want %q", got, want)
	}
	if got := afterShrink[2]; got != nextLine {
		t.Fatalf("after shrink row 2 = %q, want hard-newline row %q", got, nextLine)
	}

	emu.Resize(width, height)
	afterWiden := EmulatorContentLines(emu)
	if got := afterWiden[0]; got != fullWidthLine {
		t.Fatalf("after widen row 0 = %q, want %q", got, fullWidthLine)
	}
	if got := afterWiden[1]; got != nextLine {
		t.Fatalf("after widen row 1 = %q, want hard-newline row %q", got, nextLine)
	}
}

func TestVTEmulatorResizeShrinkKeepsCursorLineVisible(t *testing.T) {
	t.Parallel()

	const (
		width       = 40
		shrinkWidth = 10
		height      = 4
	)
	emu := NewVTEmulatorWithDrain(width, height)
	longLine := strings.Repeat("x", width*2)
	prompt := "PROMPT$ "
	mustWrite(t, emu, []byte("\x1b[2J\x1b[H"+longLine+"\r\n"+prompt))

	emu.Resize(shrinkWidth, height)
	afterShrink := EmulatorContentLines(emu)
	if !strings.Contains(strings.Join(afterShrink, "\n"), strings.TrimRight(prompt, " ")) {
		t.Fatalf("after shrink rows = %#v, want cursor line containing %q to remain visible", afterShrink, prompt)
	}
}

func TestVTEmulatorResizeShrinkPreservesActiveStyle(t *testing.T) {
	t.Parallel()

	const (
		width       = 20
		shrinkWidth = 12
		height      = 4
	)
	emu := NewVTEmulatorWithDrain(width, height)
	mustWrite(t, emu, []byte("\x1b[31mABCDEFGHIJKLMNOPQRST"))

	emu.Resize(shrinkWidth, height)
	mustWrite(t, emu, []byte("Z"))

	screenWidth, screenHeight := emu.Size()
	for y := 0; y < screenHeight; y++ {
		for x := 0; x < screenWidth; x++ {
			cell := emu.CellAt(x, y)
			if cell == nil || cell.Content != "Z" {
				continue
			}
			if cell.Style.Fg == nil {
				t.Fatalf("CellAt(%d, %d).Style.Fg = nil, want active red style preserved after resize", x, y)
			}
			return
		}
	}
	t.Fatalf("screen after shrink and write did not contain Z: %#v", EmulatorContentLines(emu))
}

func TestVTEmulatorResizeShrinkPreservesActiveHyperlink(t *testing.T) {
	t.Parallel()

	const (
		width       = 20
		shrinkWidth = 12
		height      = 4
		linkURL     = "https://example.test"
	)
	emu := NewVTEmulatorWithDrain(width, height)
	mustWrite(t, emu, []byte("\x1b[2J\x1b[HABCDEFGHIJKLMNOPQRST"+ansi.SetHyperlink(linkURL)))

	emu.Resize(shrinkWidth, height)
	cell := emu.CellAt(0, 0)
	if cell == nil {
		t.Fatal("CellAt(0, 0) = nil, want repainted cell")
	}
	if cell.Link.URL != "" {
		t.Fatalf("CellAt(0, 0).Link.URL = %q, want resized cells not to leak active hyperlink", cell.Link.URL)
	}

	mustWrite(t, emu, []byte("Z"))
	screenWidth, screenHeight := emu.Size()
	for y := 0; y < screenHeight; y++ {
		for x := 0; x < screenWidth; x++ {
			cell := emu.CellAt(x, y)
			if cell == nil || cell.Content != "Z" {
				continue
			}
			if cell.Link.URL != linkURL {
				t.Fatalf("CellAt(%d, %d).Link.URL = %q, want active hyperlink preserved", x, y, cell.Link.URL)
			}
			return
		}
	}
	t.Fatalf("screen after shrink and write did not contain Z: %#v", EmulatorContentLines(emu))
}

func TestVTEmulatorResizeShrinkPreservesPartialEscapeSequence(t *testing.T) {
	t.Parallel()

	const (
		width       = 20
		shrinkWidth = 12
		height      = 4
	)
	emu := NewVTEmulatorWithDrain(width, height)
	mustWrite(t, emu, []byte("ABCDEFGHIJKLMNOPQRST\x1b[31"))

	emu.Resize(shrinkWidth, height)
	mustWrite(t, emu, []byte("mZ"))

	screenWidth, screenHeight := emu.Size()
	for y := 0; y < screenHeight; y++ {
		for x := 0; x < screenWidth; x++ {
			cell := emu.CellAt(x, y)
			if cell == nil || cell.Content != "Z" {
				continue
			}
			if cell.Style.Fg == nil {
				t.Fatalf("CellAt(%d, %d).Style.Fg = nil, want partial red SGR to survive resize", x, y)
			}
			return
		}
	}
	t.Fatalf("screen after shrink and partial SGR completion did not contain Z: %#v", EmulatorContentLines(emu))
}

func resizeSmearReproLine(i, width int) string {
	letter := string(rune('A' + i - 1))
	prefix := fmt.Sprintf("LINE_%d_BEGIN_", i)
	suffix := fmt.Sprintf("_END_%d", i)
	return prefix + strings.Repeat(letter, width-len(prefix)-len(suffix)) + suffix
}
