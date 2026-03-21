package client

import (
	"bytes"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestNormalizeLocalInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []byte
		want  []byte
	}{
		{
			name:  "kitty ctrl-d c0",
			input: []byte("\x1b[4;5u"),
			want:  []byte{0x04},
		},
		{
			name:  "kitty ctrl-u c0",
			input: []byte("\x1b[21;5u"),
			want:  []byte{0x15},
		},
		{
			name:  "kitty ctrl-a",
			input: []byte("\x1b[97;5u"),
			want:  []byte{0x01},
		},
		{
			name:  "kitty alt-h",
			input: []byte("\x1b[104;3u"),
			want:  []byte{0x1b, 'h'},
		},
		{
			name:  "kitty escape",
			input: []byte("\x1b[27u"),
			want:  []byte{0x1b},
		},
		{
			name:  "kitty shifted printable",
			input: []byte("\x1b[97;2;65u"),
			want:  []byte("A"),
		},
		{
			name:  "unmapped kitty super key stays raw",
			input: []byte("\x1b[97;9u"),
			want:  []byte("\x1b[97;9u"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := normalizeLocalInput(tt.input); !bytes.Equal(got, tt.want) {
				t.Fatalf("normalizeLocalInput(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDecodeInputEventsKittyCtrlA(t *testing.T) {
	t.Parallel()

	events := decodeInputEvents([]byte("\x1b[97;5u"))
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}

	key, ok := events[0].event.(uv.KeyPressEvent)
	if !ok {
		t.Fatalf("event type = %T, want uv.KeyPressEvent", events[0].event)
	}
	if !key.MatchString("ctrl+a") {
		t.Fatalf("decoded key = %q, want ctrl+a", key.Keystroke())
	}
}
