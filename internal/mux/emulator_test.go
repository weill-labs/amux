package mux

import (
	"strings"
	"testing"
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

func TestRenderWithoutCursorBlock(t *testing.T) {
	t.Parallel()

	t.Run("strips isolated reverse-video space", func(t *testing.T) {
		t.Parallel()
		emu := NewVTEmulator(40, 10)
		// Write prompt, then a reverse-video space (simulating a block cursor)
		emu.Write([]byte("hello \033[7m \033[m"))

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
}

func TestHasCursorBlock(t *testing.T) {
	t.Parallel()

	t.Run("true for isolated reverse-video space", func(t *testing.T) {
		t.Parallel()
		emu := NewVTEmulator(40, 10)
		emu.Write([]byte("hello \033[7m \033[m"))
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
