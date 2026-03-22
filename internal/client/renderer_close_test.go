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
