package client

import (
	"bytes"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weill-labs/amux/internal/proto"
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
			name:  "kitty ctrl-shift-a falls back to ctrl-a",
			input: []byte("\x1b[97;6;65u"),
			want:  []byte{0x01},
		},
		{
			name:  "kitty ctrl-9 falls back to printable byte",
			input: []byte("\x1b[57;5u"),
			want:  []byte("9"),
		},
		{
			name:  "kitty ctrl-3 falls back to escape",
			input: []byte("\x1b[51;5u"),
			want:  []byte{0x1b},
		},
		{
			name:  "kitty ctrl-slash falls back to unit separator",
			input: []byte("\x1b[47;5u"),
			want:  []byte{0x1f},
		},
		{
			name:  "kitty alt-h",
			input: []byte("\x1b[104;3u"),
			want:  []byte{0x1b, 'h'},
		},
		{
			name:  "kitty alt-shift-a preserves shifted printable",
			input: []byte("\x1b[97;4;65u"),
			want:  []byte{0x1b, 'A'},
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

func TestDecodeInputEventsFocusAndBlur(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []byte
		want  any
	}{
		{
			name:  "focus",
			input: []byte("\x1b[I"),
			want:  uv.FocusEvent{},
		},
		{
			name:  "blur",
			input: []byte("\x1b[O"),
			want:  uv.BlurEvent{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			events := decodeInputEvents(tt.input)
			if len(events) != 1 {
				t.Fatalf("len(events) = %d, want 1", len(events))
			}
			if got := events[0].event; got != tt.want {
				t.Fatalf("event = %T, want %T", got, tt.want)
			}
		})
	}
}

func TestClientUIEventForDecodedInput(t *testing.T) {
	t.Parallel()

	focus := decodeInputEvents([]byte("\x1b[I"))[0]
	if got, handled := clientUIEventForDecodedInput(focus); !handled || got != proto.UIEventClientFocusGained {
		t.Fatalf("focus ui event = (%q, %v), want (%q, true)", got, handled, proto.UIEventClientFocusGained)
	}

	blur := decodeInputEvents([]byte("\x1b[O"))[0]
	if got, handled := clientUIEventForDecodedInput(blur); !handled || got != "" {
		t.Fatalf("blur ui event = (%q, %v), want (\"\", true)", got, handled)
	}

	key := decodeInputEvents([]byte("x"))[0]
	if got, handled := clientUIEventForDecodedInput(key); handled || got != "" {
		t.Fatalf("keypress ui event = (%q, %v), want (\"\", false)", got, handled)
	}
}

func TestHasActivityInputIgnoresFocusEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []byte
		want  bool
	}{
		{
			name:  "focus only",
			input: []byte("\x1b[I"),
			want:  false,
		},
		{
			name:  "blur only",
			input: []byte("\x1b[O"),
			want:  false,
		},
		{
			name:  "focus plus key",
			input: []byte("\x1b[Ix"),
			want:  true,
		},
		{
			name:  "plain key",
			input: []byte("x"),
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := hasActivityInput(tt.input); got != tt.want {
				t.Fatalf("hasActivityInput(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestForwardedBytesForDecodedInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []byte
		want  []byte
	}{
		{
			name:  "kitty ctrl-c preserves raw csi-u bytes",
			input: []byte("\x1b[99;5u"),
			want:  []byte("\x1b[99;5u"),
		},
		{
			name:  "kitty ctrl-shift-a preserves raw csi-u bytes",
			input: []byte("\x1b[97;6;65u"),
			want:  []byte("\x1b[97;6;65u"),
		},
		{
			name:  "kitty ctrl-9 preserves raw csi-u bytes",
			input: []byte("\x1b[57;5u"),
			want:  []byte("\x1b[57;5u"),
		},
		{
			name:  "kitty ctrl-slash preserves raw csi-u bytes",
			input: []byte("\x1b[47;5u"),
			want:  []byte("\x1b[47;5u"),
		},
		{
			name:  "kitty alt-h preserves raw csi-u bytes",
			input: []byte("\x1b[104;3u"),
			want:  []byte("\x1b[104;3u"),
		},
		{
			name:  "kitty alt-shift-a preserves raw csi-u bytes",
			input: []byte("\x1b[97;4;65u"),
			want:  []byte("\x1b[97;4;65u"),
		},
		{
			name:  "kitty alt-up preserves raw special-key bytes",
			input: []byte("\x1b[1;3A"),
			want:  []byte("\x1b[1;3A"),
		},
		{
			name:  "kitty composed key preserves associated text sequence",
			input: []byte("\x1b[97;;229u"),
			want:  []byte("\x1b[97;;229u"),
		},
		{
			name:  "plain text stays unchanged",
			input: []byte("x"),
			want:  []byte("x"),
		},
		{
			name:  "unmapped kitty key stays raw",
			input: []byte("\x1b[97;9u"),
			want:  []byte("\x1b[97;9u"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			events := decodeInputEvents(tt.input)
			if len(events) != 1 {
				t.Fatalf("len(events) = %d, want 1", len(events))
			}
			if got := forwardedBytesForDecodedInput(events[0]); !bytes.Equal(got, tt.want) {
				t.Fatalf("forwardedBytesForDecodedInput(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestForwardedBytesForDecodedInputUsesRawForNonKeyPress(t *testing.T) {
	t.Parallel()

	raw := []byte("paste")
	decoded := decodedInputEvent{raw: raw}
	if got := forwardedBytesForDecodedInput(decoded); !bytes.Equal(got, raw) {
		t.Fatalf("forwardedBytesForDecodedInput(non-keypress) = %q, want %q", got, raw)
	}
}

func TestForwardedBytesForDecodedInputFallsBackToRawWhenNormalizationIsEmpty(t *testing.T) {
	t.Parallel()

	decoded := decodedInputEvent{
		event: uv.KeyPressEvent{},
		raw:   nil,
	}
	if got := forwardedBytesForDecodedInput(decoded); len(got) != 0 {
		t.Fatalf("forwardedBytesForDecodedInput(empty raw) = %q, want empty", got)
	}
}

func TestLegacyBytesForKeyPressUsesAltSpecialSequence(t *testing.T) {
	t.Parallel()

	key := uv.KeyPressEvent{Code: uv.KeyUp, Mod: uv.ModAlt}
	want := []byte{0x1b, 0x1b, '[', 'A'}
	if got := legacyBytesForKeyPress(key); !bytes.Equal(got, want) {
		t.Fatalf("legacyBytesForKeyPress(alt+up) = %q, want %q", got, want)
	}
}
