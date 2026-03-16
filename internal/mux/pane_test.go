package mux

import (
	"testing"
)

func TestContentLines(t *testing.T) {
	emu := NewVTEmulatorWithDrain(40, 5)

	p := &Pane{
		ID:       1,
		emulator: emu,
	}

	// Write two lines of content
	emu.Write([]byte("hello world\r\nline two\r\n"))

	lines := p.ContentLines()

	if len(lines) != 5 {
		t.Fatalf("expected 5 lines (pane height), got %d", len(lines))
	}
	if lines[0] != "hello world" {
		t.Errorf("line 0: got %q, want %q", lines[0], "hello world")
	}
	if lines[1] != "line two" {
		t.Errorf("line 1: got %q, want %q", lines[1], "line two")
	}
	// Remaining lines should be empty
	for i := 2; i < 5; i++ {
		if lines[i] != "" {
			t.Errorf("line %d: got %q, want empty", i, lines[i])
		}
	}
}

func TestContentLinesStripsANSI(t *testing.T) {
	emu := NewVTEmulatorWithDrain(40, 3)

	p := &Pane{
		ID:       1,
		emulator: emu,
	}

	// Write colored text
	emu.Write([]byte("\033[31mRED\033[m normal\r\n"))

	lines := p.ContentLines()

	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if lines[0] != "RED normal" {
		t.Errorf("line 0: got %q, want %q", lines[0], "RED normal")
	}
}
