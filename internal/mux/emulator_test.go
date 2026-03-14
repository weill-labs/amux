package mux

import (
	"strings"
	"testing"
)

func TestVTEmulatorWriteRender(t *testing.T) {
	emu := NewVTEmulator(80, 24)

	// Write some text
	emu.Write([]byte("Hello, world!"))

	rendered := emu.Render()
	if !strings.Contains(rendered, "Hello, world!") {
		t.Errorf("Render() = %q, want to contain %q", rendered, "Hello, world!")
	}
}

func TestVTEmulatorSize(t *testing.T) {
	emu := NewVTEmulator(120, 40)

	w, h := emu.Size()
	if w != 120 || h != 40 {
		t.Errorf("Size() = (%d, %d), want (120, 40)", w, h)
	}
}

func TestVTEmulatorResize(t *testing.T) {
	emu := NewVTEmulator(80, 24)
	emu.Resize(120, 40)

	w, h := emu.Size()
	if w != 120 || h != 40 {
		t.Errorf("after Resize: Size() = (%d, %d), want (120, 40)", w, h)
	}
}

func TestVTEmulatorCursorPosition(t *testing.T) {
	emu := NewVTEmulator(80, 24)

	// Write text to move cursor
	emu.Write([]byte("ABCD"))

	col, row := emu.CursorPosition()
	if col != 4 || row != 0 {
		t.Errorf("CursorPosition() = (%d, %d), want (4, 0)", col, row)
	}
}

func TestRenderWithCursor(t *testing.T) {
	emu := NewVTEmulator(80, 24)
	emu.Write([]byte("test"))

	result := RenderWithCursor(emu)
	// Should end with cursor positioning sequence
	if !strings.Contains(result, "\033[") {
		t.Error("RenderWithCursor should contain ANSI cursor positioning")
	}
}
