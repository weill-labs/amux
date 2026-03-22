package mux

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/weill-labs/amux/internal/mouse"
)

func TestVTEmulatorWriteRender(t *testing.T) {
	t.Parallel()
	emu := NewVTEmulator(80, 24)
	emu.Write([]byte("Hello, world!"))

	rendered := emu.Render()
	if !strings.Contains(rendered, "Hello, world!") {
		t.Errorf("Render() = %q, want to contain %q", rendered, "Hello, world!")
	}
}

func TestVTEmulatorSize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		initW, initH     int
		resizeW, resizeH int // 0 means no resize
		wantW, wantH     int
	}{
		{"initial 80x24", 80, 24, 0, 0, 80, 24},
		{"initial 120x40", 120, 40, 0, 0, 120, 40},
		{"resize 80x24 to 120x40", 80, 24, 120, 40, 120, 40},
		{"resize 120x40 to 40x10", 120, 40, 40, 10, 40, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			emu := NewVTEmulator(tt.initW, tt.initH)
			if tt.resizeW > 0 {
				emu.Resize(tt.resizeW, tt.resizeH)
			}
			w, h := emu.Size()
			if w != tt.wantW || h != tt.wantH {
				t.Errorf("Size() = (%d, %d), want (%d, %d)", w, h, tt.wantW, tt.wantH)
			}
		})
	}
}

func TestVTEmulatorCursorPosition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		wantCol int
		wantRow int
	}{
		{"after ABCD", "ABCD", 4, 0},
		{"empty", "", 0, 0},
		{"after newline", "hello\r\n", 0, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			emu := NewVTEmulator(80, 24)
			if tt.input != "" {
				emu.Write([]byte(tt.input))
			}
			col, row := emu.CursorPosition()
			if col != tt.wantCol || row != tt.wantRow {
				t.Errorf("CursorPosition() = (%d, %d), want (%d, %d)", col, row, tt.wantCol, tt.wantRow)
			}
		})
	}
}

func TestRenderWithCursor(t *testing.T) {
	t.Parallel()
	emu := NewVTEmulator(80, 24)
	emu.Write([]byte("test"))

	result := RenderWithCursor(emu)
	if !strings.Contains(result, "\033[") {
		t.Error("RenderWithCursor should contain ANSI cursor positioning")
	}
}

func TestRenderWithCursorRoundTrip(t *testing.T) {
	t.Parallel()
	// Verify that RenderWithCursor output replays correctly into a fresh
	// emulator. Catches width-dependent wrapping with wide Unicode chars
	// (block elements, box drawing) that caused garbled reattach rendering.
	content := []string{
		"           Claude Code v2.1.76",
		" \u2590\u259b\u2588\u2588\u2588\u259c\u258c   Opus 4.6 (1M context)",
		"\u259d\u259c\u2588\u2588\u2588\u2588\u2588\u259b\u2598  Claude Max",
		"  \u2598\u2598 \u259d\u259d    /Users/test",
		"",
		"\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500",
		"\u276f ",
	}

	w, h := 44, 20
	emu1 := NewVTEmulatorWithDrain(w, h)
	for i, line := range content {
		emu1.Write([]byte(fmt.Sprintf("\033[%d;1H%s", i+1, line)))
	}

	emu2 := NewVTEmulatorWithDrain(w, h)
	emu2.Write([]byte(RenderWithCursor(emu1)))

	lines1 := strings.Split(emu1.Render(), "\n")
	lines2 := strings.Split(emu2.Render(), "\n")

	for i := 0; i < len(lines1) && i < len(lines2); i++ {
		s1 := StripANSI(lines1[i])
		s2 := StripANSI(lines2[i])
		if s1 != s2 {
			t.Errorf("line %d mismatch:\n  orig:   %q\n  replay: %q", i, s1, s2)
		}
	}
}

func TestRenderWithoutCursorBlock(t *testing.T) {
	t.Parallel()

	t.Run("strips isolated reverse-video space", func(t *testing.T) {
		t.Parallel()
		emu := NewVTEmulator(40, 10)
		// Write prompt, then a reverse-video space (simulating a block cursor)
		emu.Write([]byte("hello \033[7m \033[m\033[1D"))

		// Normal Render should contain reverse video
		normal := emu.Render()
		if !strings.Contains(normal, "\033[7m") {
			t.Fatal("Render() should contain reverse video sequence")
		}

		// RenderWithoutCursorBlock should strip the isolated reverse-video space
		stripped := emu.RenderWithoutCursorBlock()
		if strings.Contains(stripped, "\033[7m") {
			t.Error("RenderWithoutCursorBlock() should not contain reverse video for cursor block")
		}

		// Normal Render should still work (cells were restored)
		after := emu.Render()
		if !strings.Contains(after, "\033[7m") {
			t.Error("Render() after RenderWithoutCursorBlock should still contain reverse video")
		}
	})

	t.Run("preserves multi-character reverse video", func(t *testing.T) {
		t.Parallel()
		emu := NewVTEmulator(40, 10)
		// Write a selection-like highlight (multiple reverse-video characters)
		emu.Write([]byte("\033[7mselected\033[m"))

		stripped := emu.RenderWithoutCursorBlock()
		if !strings.Contains(stripped, "\033[7m") {
			t.Error("RenderWithoutCursorBlock() should preserve multi-char reverse video")
		}
	})

	t.Run("no-op when cursor cell has no reverse video", func(t *testing.T) {
		t.Parallel()
		emu := NewVTEmulator(40, 10)
		emu.Write([]byte("hello"))

		normal := emu.Render()
		stripped := emu.RenderWithoutCursorBlock()
		if normal != stripped {
			t.Error("RenderWithoutCursorBlock() should match Render() when no reverse video at cursor")
		}
	})

	t.Run("preserves isolated reverse-video space away from cursor", func(t *testing.T) {
		t.Parallel()
		emu := NewVTEmulator(40, 10)
		emu.Write([]byte("hello \033[7m \033[m"))
		emu.Write([]byte("\033[1;1H"))

		stripped := emu.RenderWithoutCursorBlock()
		if !strings.Contains(stripped, "\033[7m") {
			t.Fatal("RenderWithoutCursorBlock() should preserve off-cursor reverse video")
		}
	})
}

func TestHasCursorBlock(t *testing.T) {
	t.Parallel()

	t.Run("true for isolated reverse-video space", func(t *testing.T) {
		t.Parallel()
		emu := NewVTEmulator(40, 10)
		emu.Write([]byte("hello \033[7m \033[m\033[1D"))
		if !emu.HasCursorBlock() {
			t.Error("HasCursorBlock() = false, want true")
		}
	})

	t.Run("false for adjacent reverse-video content", func(t *testing.T) {
		t.Parallel()
		emu := NewVTEmulator(40, 10)
		emu.Write([]byte("\033[7mselected\033[m"))
		if emu.HasCursorBlock() {
			t.Error("HasCursorBlock() = true for multi-char reverse video, want false")
		}
	})

	t.Run("false when no reverse video", func(t *testing.T) {
		t.Parallel()
		emu := NewVTEmulator(40, 10)
		emu.Write([]byte("hello"))
		if emu.HasCursorBlock() {
			t.Error("HasCursorBlock() = true with no reverse video, want false")
		}
	})

	t.Run("false for isolated reverse-video space away from cursor", func(t *testing.T) {
		t.Parallel()
		emu := NewVTEmulator(40, 10)
		emu.Write([]byte("hello \033[7m \033[m"))
		emu.Write([]byte("\033[1;1H"))
		if emu.HasCursorBlock() {
			t.Error("HasCursorBlock() = true for off-cursor reverse video, want false")
		}
	})

	t.Run("true for isolated reverse-video space above lower-left reported cursor", func(t *testing.T) {
		t.Parallel()
		emu := NewVTEmulator(40, 10)
		emu.Write([]byte("hello \033[7m \033[m"))
		emu.Write([]byte("\033[2;1H"))
		if !emu.HasCursorBlock() {
			t.Error("HasCursorBlock() = false with fallback cursor block, want true")
		}
	})
}

func TestRenderWithoutCursorBlockFallsBackFromLowerLeftCursor(t *testing.T) {
	t.Parallel()

	emu := NewVTEmulator(40, 10)
	emu.Write([]byte("hello \033[7m \033[m"))
	emu.Write([]byte("\033[2;1H"))

	stripped := emu.RenderWithoutCursorBlock()
	if strings.Contains(stripped, "\033[7m") {
		t.Fatal("RenderWithoutCursorBlock() should strip fallback cursor block")
	}

	if normal := emu.Render(); !strings.Contains(normal, "\033[7m") {
		t.Fatal("Render() should preserve reverse-video block after fallback stripping")
	}
}

func TestMouseProtocolTracking(t *testing.T) {
	t.Parallel()

	emu := NewVTEmulator(40, 10)

	if got := emu.MouseProtocol(); got.Enabled() {
		t.Fatalf("mouse protocol should start disabled, got %+v", got)
	}
	if emu.IsAltScreen() {
		t.Fatal("alternate screen should start disabled")
	}

	emu.Write([]byte("\x1b[?1049h\x1b[?1000h\x1b[?1006h"))
	got := emu.MouseProtocol()
	if !emu.IsAltScreen() {
		t.Fatal("expected alternate screen to be enabled")
	}
	if got.Tracking != MouseTrackingStandard || !got.SGR {
		t.Fatalf("MouseProtocol = %+v, want standard+SGR", got)
	}

	emu.Write([]byte("\x1b[?1000l\x1b[?1002h"))
	got = emu.MouseProtocol()
	if got.Tracking != MouseTrackingButton || !got.SGR {
		t.Fatalf("MouseProtocol after 1002h = %+v, want button+SGR", got)
	}

	emu.Write([]byte("\x1b[?1003h"))
	got = emu.MouseProtocol()
	if got.Tracking != MouseTrackingAny {
		t.Fatalf("MouseProtocol after 1003h = %+v, want any", got)
	}

	emu.Write([]byte("\x1b[?1003l\x1b[?1002l\x1b[?1006l\x1b[?1049l"))
	got = emu.MouseProtocol()
	if got.Enabled() || got.SGR {
		t.Fatalf("MouseProtocol after reset = %+v, want disabled", got)
	}
	if emu.IsAltScreen() {
		t.Fatal("expected alternate screen to be disabled")
	}
}

func TestEncodeMouse(t *testing.T) {
	t.Parallel()

	emu := NewVTEmulator(40, 10)
	ev := mouse.Event{Button: mouse.ScrollUp, Action: mouse.Press, X: 4, Y: 6}
	if got := emu.EncodeMouse(ev, 4, 6); got != nil {
		t.Fatalf("EncodeMouse without mode = %q, want nil", got)
	}

	emu.Write([]byte("\x1b[?1002h\x1b[?1006h"))
	got := string(emu.EncodeMouse(ev, 4, 6))
	if got != "\x1b[<64;5;7M" {
		t.Fatalf("EncodeMouse SGR = %q, want %q", got, "\x1b[<64;5;7M")
	}

	emu.Write([]byte("\x1b[?1006l"))
	got = string(emu.EncodeMouse(ev, 4, 6))
	if got != "\x1b[M`%'" {
		t.Fatalf("EncodeMouse X10 = %q, want %q", got, "\x1b[M`%'")
	}
}

func TestScreenLineText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		row   int
		want  string
	}{
		{"plain text row 0", "Hello, world!", 0, "Hello, world!"},
		{"colored text", "\033[31mred\033[0m \033[32mgreen\033[0m", 0, "red green"},
		{"second row", "line1\r\nline2", 1, "line2"},
		{"empty row", "hello", 5, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			emu := NewVTEmulatorWithDrain(80, 24)
			emu.Write([]byte(tt.input))
			got := emu.ScreenLineText(tt.row)
			if got != tt.want {
				t.Errorf("ScreenLineText(%d) = %q, want %q", tt.row, got, tt.want)
			}
		})
	}
}

func TestScreenLineTextWideChars(t *testing.T) {
	t.Parallel()
	emu := NewVTEmulatorWithDrain(80, 24)
	// CJK character "中" is width 2, occupies 2 cells
	emu.Write([]byte("A中B"))
	got := emu.ScreenLineText(0)
	if got != "A中B" {
		t.Errorf("ScreenLineText(0) = %q, want %q", got, "A中B")
	}
}

func TestScreenLineTextGraphemeClusters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		input         []byte
		wantLine      string
		wantCell0     string
		wantCell0Wide int
	}{
		{
			name:          "combining mark cluster",
			input:         []byte("Λ\u030A1"),
			wantLine:      "Λ\u030A1",
			wantCell0:     "Λ\u030A",
			wantCell0Wide: 1,
		},
		{
			name:          "emoji modifier cluster",
			input:         []byte("👍🏻2"),
			wantLine:      "👍🏻2",
			wantCell0:     "👍🏻",
			wantCell0Wide: 2,
		},
		{
			name:          "zwj emoji cluster",
			input:         []byte("🤷‍♂️3"),
			wantLine:      "🤷‍♂️3",
			wantCell0:     "🤷‍♂️",
			wantCell0Wide: 2,
		},
		{
			name:          "regional indicator flag cluster",
			input:         []byte("🇸🇪4"),
			wantLine:      "🇸🇪4",
			wantCell0:     "🇸🇪",
			wantCell0Wide: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			emu := NewVTEmulatorWithDrain(20, 5)
			emu.Write(tt.input)

			if got := emu.ScreenLineText(0); got != tt.wantLine {
				t.Fatalf("ScreenLineText(0) = %q, want %q", got, tt.wantLine)
			}

			cell0 := emu.CellAt(0, 0)
			if cell0 == nil {
				t.Fatal("CellAt(0, 0) = nil, want grapheme cluster")
			}
			if cell0.Content != tt.wantCell0 || cell0.Width != tt.wantCell0Wide {
				t.Fatalf("CellAt(0, 0) = {content:%q width:%d}, want {content:%q width:%d}",
					cell0.Content, cell0.Width, tt.wantCell0, tt.wantCell0Wide)
			}

			if tt.wantCell0Wide == 2 {
				cont := emu.CellAt(1, 0)
				if cont == nil {
					t.Fatal("CellAt(1, 0) = nil, want continuation cell")
				}
				if cont.Width != 0 {
					t.Fatalf("CellAt(1, 0).Width = %d, want 0 continuation", cont.Width)
				}
			}
		})
	}
}

func TestScreenLineTextTrailingSpaces(t *testing.T) {
	t.Parallel()
	emu := NewVTEmulatorWithDrain(80, 24)
	emu.Write([]byte("hello"))
	got := emu.ScreenLineText(0)
	// Should not have trailing spaces (the remaining 75 columns)
	if got != "hello" {
		t.Errorf("ScreenLineText(0) = %q, want %q", got, "hello")
	}
}

func TestScrollbackCellAt(t *testing.T) {
	t.Parallel()

	emu := NewVTEmulatorWithDrainAndScrollback(20, 1, 10)
	if _, err := emu.Write([]byte("\033[31mred\033[0m\r\nnext")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	cell := emu.ScrollbackCellAt(0, 0)
	if cell == nil {
		t.Fatal("ScrollbackCellAt(0, 0) = nil, want styled cell")
	}
	if cell.Content != "r" {
		t.Fatalf("ScrollbackCellAt(0, 0).Content = %q, want %q", cell.Content, "r")
	}
	if cell.Style.Fg == nil {
		t.Fatal("ScrollbackCellAt(0, 0).Style.Fg = nil, want red")
	}
	gotR, gotG, gotB, gotA := cell.Style.Fg.RGBA()
	wantR, wantG, wantB, wantA := ansi.BasicColor(1).RGBA()
	if gotR != wantR || gotG != wantG || gotB != wantB || gotA != wantA {
		t.Fatalf("ScrollbackCellAt(0, 0).Style.Fg = (%d,%d,%d,%d), want (%d,%d,%d,%d)",
			gotR, gotG, gotB, gotA, wantR, wantG, wantB, wantA)
	}
	if got := emu.ScrollbackCellAt(99, 0); got != nil {
		t.Fatalf("ScrollbackCellAt(99, 0) = %#v, want nil", got)
	}
	if got := emu.ScrollbackCellAt(-1, 0); got != nil {
		t.Fatalf("ScrollbackCellAt(-1, 0) = %#v, want nil", got)
	}
}

func TestScreenContains(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		substr string
		want   bool
	}{
		{"match", "hello world", "world", true},
		{"no match", "hello world", "missing", false},
		{"empty substr", "hello", "", true},
		{"colored match", "\033[31mhello\033[0m", "hello", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			emu := NewVTEmulatorWithDrain(80, 24)
			emu.Write([]byte(tt.input))
			got := emu.ScreenContains(tt.substr)
			if got != tt.want {
				t.Errorf("ScreenContains(%q) = %v, want %v", tt.substr, got, tt.want)
			}
		})
	}
}

func TestScreenContainsMultiRow(t *testing.T) {
	t.Parallel()
	emu := NewVTEmulatorWithDrain(80, 24)
	emu.Write([]byte("first line\r\nsecond line\r\nthird line"))

	if !emu.ScreenContains("second") {
		t.Error("ScreenContains(\"second\") = false, want true")
	}
	if !emu.ScreenContains("third") {
		t.Error("ScreenContains(\"third\") = false, want true")
	}
	if emu.ScreenContains("fourth") {
		t.Error("ScreenContains(\"fourth\") = true, want false")
	}
}

func TestScreenContainsSoftWrap(t *testing.T) {
	t.Parallel()
	// Write a string longer than the terminal width (20 cols). The emulator
	// soft-wraps at column 20, splitting the text across two screen rows.
	// ScreenContains should still find the full substring.
	emu := NewVTEmulatorWithDrain(20, 5)
	emu.Write([]byte("cannot attach recursive nesting"))

	if !emu.ScreenContains("recursive nesting") {
		t.Error("ScreenContains should match across soft-wrapped lines")
	}
	// Verify per-line still works for non-wrapped content
	if !emu.ScreenContains("cannot") {
		t.Error("ScreenContains should still match within a single line")
	}
	// Negative case: substring not present at all
	if emu.ScreenContains("missing text") {
		t.Error("ScreenContains should not match absent text")
	}
}

func TestCursorHidden(t *testing.T) {
	t.Parallel()

	t.Run("visible by default", func(t *testing.T) {
		t.Parallel()
		emu := NewVTEmulator(80, 24)
		if emu.CursorHidden() {
			t.Error("CursorHidden() = true on fresh emulator, want false")
		}
	})

	t.Run("hidden after hide sequence", func(t *testing.T) {
		t.Parallel()
		emu := NewVTEmulator(80, 24)
		emu.Write([]byte("\033[?25l")) // hide cursor
		if !emu.CursorHidden() {
			t.Error("CursorHidden() = false after \\033[?25l, want true")
		}
	})

	t.Run("visible after show sequence", func(t *testing.T) {
		t.Parallel()
		emu := NewVTEmulator(80, 24)
		emu.Write([]byte("\033[?25l")) // hide
		emu.Write([]byte("\033[?25h")) // show
		if emu.CursorHidden() {
			t.Error("CursorHidden() = true after \\033[?25h, want false")
		}
	})
}
