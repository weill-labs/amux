package client

import (
	"testing"

	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

func TestTerminalEnterSequence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		caps proto.ClientCapabilities
		want string
	}{
		{
			name: "legacy",
			want: render.AltScreenEnter + render.MouseEnable,
		},
		{
			name: "kitty keyboard",
			caps: proto.ClientCapabilities{KittyKeyboard: true},
			want: render.AltScreenEnter + render.MouseEnable + render.KittyKeyboardEnable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := terminalEnterSequence(tt.caps); got != tt.want {
				t.Fatalf("terminalEnterSequence() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTerminalExitSequence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		caps proto.ClientCapabilities
		want string
	}{
		{
			name: "legacy",
			want: render.MouseDisable + render.AltScreenExit + render.ResetTitle,
		},
		{
			name: "kitty keyboard",
			caps: proto.ClientCapabilities{KittyKeyboard: true},
			want: render.KittyKeyboardDisable + render.MouseDisable + render.AltScreenExit + render.ResetTitle,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := terminalExitSequence(tt.caps); got != tt.want {
				t.Fatalf("terminalExitSequence() = %q, want %q", got, tt.want)
			}
		})
	}
}
