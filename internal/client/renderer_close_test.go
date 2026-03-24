package client

import (
	"io"
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

func TestRendererCloseClosesPaneEmulators(t *testing.T) {
	r := NewWithScrollback(20, 4, mux.DefaultScrollbackLines)
	r.HandleLayout(singlePane20x3())

	emu, ok := r.Emulator(1)
	if !ok {
		t.Fatal("expected pane emulator")
	}

	r.Close()
	r.Close()

	if _, err := emu.Write([]byte("x")); err != io.ErrClosedPipe {
		t.Fatalf("emu.Write after Close() = %v, want %v", err, io.ErrClosedPipe)
	}
	if _, err := emu.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("emu.Read after Close() = %v, want %v", err, io.EOF)
	}
}

func TestHandleLayoutClosesRemovedPaneEmulators(t *testing.T) {
	t.Parallel()
	r := NewWithScrollback(80, 24, mux.DefaultScrollbackLines)

	// Start with two panes.
	r.HandleLayout(twoPane80x23())

	emu2, ok := r.Emulator(2)
	if !ok {
		t.Fatal("expected pane-2 emulator after two-pane layout")
	}

	// Transition to single pane — pane 2 is removed.
	r.HandleLayout(singlePane20x3())

	// The removed emulator's pipe should be closed.
	if _, err := emu2.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("removed emulator Read() = %v, want io.EOF", err)
	}

	// Surviving emulator should still work.
	emu1, ok := r.Emulator(1)
	if !ok {
		t.Fatal("expected pane-1 emulator after layout transition")
	}
	if _, err := emu1.Write([]byte("\x1b[6n")); err != nil {
		t.Fatalf("surviving emulator Write() = %v", err)
	}

	r.Close()
}
