package client

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weill-labs/amux/internal/proto"
)

func decodeSingleInputEvent(t *testing.T, input []byte) decodedInputEvent {
	t.Helper()

	events := decodeInputEvents(input)
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}

	return events[0]
}

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

func TestSplitTrailingIncompleteUTF8(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        []byte
		wantComplete []byte
		wantPending  []byte
	}{
		{
			name:         "ascii stays complete",
			input:        []byte("hello"),
			wantComplete: []byte("hello"),
		},
		{
			name:         "complete multibyte rune stays complete",
			input:        []byte("→"),
			wantComplete: []byte("→"),
		},
		{
			name:         "three-byte prefix is deferred",
			input:        []byte("A\xe2"),
			wantComplete: []byte("A"),
			wantPending:  []byte{0xe2},
		},
		{
			name:        "emoji prefix is deferred",
			input:       []byte{0xf0, 0x9f},
			wantPending: []byte{0xf0, 0x9f},
		},
		{
			name:         "invalid continuation byte is preserved",
			input:        []byte{0x80},
			wantComplete: []byte{0x80},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotComplete, gotPending := splitTrailingIncompleteUTF8(tt.input)
			if !bytes.Equal(gotComplete, tt.wantComplete) {
				t.Fatalf("complete = %q, want %q", gotComplete, tt.wantComplete)
			}
			if !bytes.Equal(gotPending, tt.wantPending) {
				t.Fatalf("pending = %q, want %q", gotPending, tt.wantPending)
			}
		})
	}
}

func TestSplitTrailingIncompleteUTF8RoundTripsChunkedPaste(t *testing.T) {
	t.Parallel()

	payload := []byte(strings.Repeat("→ 72°F — 22°C — 🙂漢字\n", 128))
	chunkSizes := []int{2, 3, 5, 4095, 4097}

	for _, chunkSize := range chunkSizes {
		chunkSize := chunkSize
		t.Run(strconv.Itoa(chunkSize), func(t *testing.T) {
			t.Parallel()

			var (
				got     []byte
				pending []byte
			)

			for start := 0; start < len(payload); start += chunkSize {
				end := start + chunkSize
				if end > len(payload) {
					end = len(payload)
				}
				chunk := append(append([]byte(nil), pending...), payload[start:end]...)
				ready, tail := splitTrailingIncompleteUTF8(chunk)
				got = append(got, ready...)
				pending = append(pending[:0], tail...)
			}
			got = append(got, pending...)

			if !bytes.Equal(got, payload) {
				t.Fatalf("round-tripped payload mismatch for chunk size %d", chunkSize)
			}
		})
	}
}

func TestDecodeInputEventsKittyCtrlA(t *testing.T) {
	t.Parallel()

	event := decodeSingleInputEvent(t, []byte("\x1b[97;5u")).event
	key, ok := event.(uv.KeyPressEvent)
	if !ok {
		t.Fatalf("event type = %T, want uv.KeyPressEvent", event)
	}
	if !key.MatchString("ctrl+a") {
		t.Fatalf("decoded key = %q, want ctrl+a", key.Keystroke())
	}
}

func TestDecodeInputEventsKeyNameMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []byte
		wantCode rune
		wantName string
	}{
		{name: "enter", input: []byte{'\r'}, wantCode: uv.KeyEnter, wantName: "enter"},
		{name: "tab", input: []byte{'\t'}, wantCode: uv.KeyTab, wantName: "tab"},
		{name: "escape", input: []byte{0x1b}, wantCode: uv.KeyEscape, wantName: "esc"},
		{name: "backspace", input: []byte{0x7f}, wantCode: uv.KeyBackspace, wantName: "backspace"},
		{name: "shift tab", input: []byte("\x1b[Z"), wantCode: uv.KeyTab, wantName: "shift+tab"},

		{name: "csi up", input: []byte("\x1b[A"), wantCode: uv.KeyUp, wantName: "up"},
		{name: "ss3 up alias", input: []byte("\x1bOA"), wantCode: uv.KeyUp, wantName: "up"},
		{name: "csi down", input: []byte("\x1b[B"), wantCode: uv.KeyDown, wantName: "down"},
		{name: "ss3 down alias", input: []byte("\x1bOB"), wantCode: uv.KeyDown, wantName: "down"},
		{name: "csi right", input: []byte("\x1b[C"), wantCode: uv.KeyRight, wantName: "right"},
		{name: "ss3 right alias", input: []byte("\x1bOC"), wantCode: uv.KeyRight, wantName: "right"},
		{name: "csi left", input: []byte("\x1b[D"), wantCode: uv.KeyLeft, wantName: "left"},
		{name: "ss3 left alias", input: []byte("\x1bOD"), wantCode: uv.KeyLeft, wantName: "left"},
		{name: "home csi", input: []byte("\x1b[H"), wantCode: uv.KeyHome, wantName: "home"},
		{name: "home ss3 alias", input: []byte("\x1bOH"), wantCode: uv.KeyHome, wantName: "home"},
		{name: "end csi", input: []byte("\x1b[F"), wantCode: uv.KeyEnd, wantName: "end"},
		{name: "end ss3 alias", input: []byte("\x1bOF"), wantCode: uv.KeyEnd, wantName: "end"},
		{name: "ctrl up", input: []byte("\x1b[1;5A"), wantCode: uv.KeyUp, wantName: "ctrl+up"},
		{name: "alt up", input: []byte("\x1b[1;3A"), wantCode: uv.KeyUp, wantName: "alt+up"},
		{name: "page up", input: []byte("\x1b[5~"), wantCode: uv.KeyPgUp, wantName: "pgup"},
		{name: "page down", input: []byte("\x1b[6~"), wantCode: uv.KeyPgDown, wantName: "pgdown"},
		{name: "insert", input: []byte("\x1b[2~"), wantCode: uv.KeyInsert, wantName: "insert"},
		{name: "delete", input: []byte("\x1b[3~"), wantCode: uv.KeyDelete, wantName: "delete"},

		{name: "f1 ss3", input: []byte("\x1bOP"), wantCode: uv.KeyF1, wantName: "f1"},
		{name: "f1 csi alias", input: []byte("\x1b[11~"), wantCode: uv.KeyF1, wantName: "f1"},
		{name: "f2 ss3", input: []byte("\x1bOQ"), wantCode: uv.KeyF2, wantName: "f2"},
		{name: "f3 ss3", input: []byte("\x1bOR"), wantCode: uv.KeyF3, wantName: "f3"},
		{name: "f4 ss3", input: []byte("\x1bOS"), wantCode: uv.KeyF4, wantName: "f4"},
		{name: "f5 csi", input: []byte("\x1b[15~"), wantCode: uv.KeyF5, wantName: "f5"},
		{name: "f6 csi", input: []byte("\x1b[17~"), wantCode: uv.KeyF6, wantName: "f6"},
		{name: "f7 csi", input: []byte("\x1b[18~"), wantCode: uv.KeyF7, wantName: "f7"},
		{name: "f8 csi", input: []byte("\x1b[19~"), wantCode: uv.KeyF8, wantName: "f8"},
		{name: "f9 csi", input: []byte("\x1b[20~"), wantCode: uv.KeyF9, wantName: "f9"},
		{name: "f10 csi", input: []byte("\x1b[21~"), wantCode: uv.KeyF10, wantName: "f10"},
		{name: "f11 csi", input: []byte("\x1b[23~"), wantCode: uv.KeyF11, wantName: "f11"},
		{name: "f12 csi", input: []byte("\x1b[24~"), wantCode: uv.KeyF12, wantName: "f12"},

		{name: "kitty ctrl a", input: []byte("\x1b[97;5u"), wantCode: 'a', wantName: "ctrl+a"},
		{name: "kitty alt shift a", input: []byte("\x1b[97;4;65u"), wantCode: 'a', wantName: "alt+shift+a"},
		{name: "kitty escape", input: []byte("\x1b[27u"), wantCode: uv.KeyEscape, wantName: "esc"},

		{name: "keypad zero application", input: []byte("\x1bOp"), wantCode: uv.KeyKp0, wantName: "0"},
		{name: "keypad one application", input: []byte("\x1bOq"), wantCode: uv.KeyKp1, wantName: "1"},
		{name: "keypad two application", input: []byte("\x1bOr"), wantCode: uv.KeyKp2, wantName: "2"},
		{name: "keypad three application", input: []byte("\x1bOs"), wantCode: uv.KeyKp3, wantName: "3"},
		{name: "keypad four application", input: []byte("\x1bOt"), wantCode: uv.KeyKp4, wantName: "4"},
		{name: "keypad five application", input: []byte("\x1bOu"), wantCode: uv.KeyKp5, wantName: "5"},
		{name: "keypad six application", input: []byte("\x1bOv"), wantCode: uv.KeyKp6, wantName: "6"},
		{name: "keypad seven application", input: []byte("\x1bOw"), wantCode: uv.KeyKp7, wantName: "7"},
		{name: "keypad eight application", input: []byte("\x1bOx"), wantCode: uv.KeyKp8, wantName: "8"},
		{name: "keypad nine application", input: []byte("\x1bOy"), wantCode: uv.KeyKp9, wantName: "9"},
		{name: "keypad enter application", input: []byte("\x1bOM"), wantCode: uv.KeyKpEnter, wantName: "enter"},
		{name: "keypad equal application", input: []byte("\x1bOX"), wantCode: uv.KeyKpEqual, wantName: "equal"},
		{name: "keypad multiply application", input: []byte("\x1bOj"), wantCode: uv.KeyKpMultiply, wantName: "mul"},
		{name: "keypad plus application", input: []byte("\x1bOk"), wantCode: uv.KeyKpPlus, wantName: "plus"},
		{name: "keypad comma application", input: []byte("\x1bOl"), wantCode: uv.KeyKpComma, wantName: "comma"},
		{name: "keypad minus application", input: []byte("\x1bOm"), wantCode: uv.KeyKpMinus, wantName: "minus"},
		{name: "keypad decimal application", input: []byte("\x1bOn"), wantCode: uv.KeyKpDecimal, wantName: "period"},
		{name: "keypad divide application", input: []byte("\x1bOo"), wantCode: uv.KeyKpDivide, wantName: "div"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			event := decodeSingleInputEvent(t, tt.input).event
			key, ok := event.(uv.KeyPressEvent)
			if !ok {
				t.Fatalf("event type = %T, want uv.KeyPressEvent", event)
			}
			if key.Code != tt.wantCode {
				t.Fatalf("decoded code = %v, want %v", key.Code, tt.wantCode)
			}
			if got := key.Keystroke(); got != tt.wantName {
				t.Fatalf("decoded name = %q, want %q", got, tt.wantName)
			}
		})
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

			if got := decodeSingleInputEvent(t, tt.input).event; got != tt.want {
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
			name:  "kitty ctrl-c translates to legacy control byte",
			input: []byte("\x1b[99;5u"),
			want:  []byte{0x03},
		},
		{
			name:  "kitty ctrl-r translates to legacy control byte",
			input: []byte("\x1b[114;5u"),
			want:  []byte{0x12},
		},
		{
			name:  "kitty ctrl-d translates to legacy control byte",
			input: []byte("\x1b[100;5u"),
			want:  []byte{0x04},
		},
		{
			name:  "kitty ctrl-z translates to legacy control byte",
			input: []byte("\x1b[122;5u"),
			want:  []byte{0x1a},
		},
		{
			name:  "kitty ctrl-l translates to legacy control byte",
			input: []byte("\x1b[108;5u"),
			want:  []byte{0x0c},
		},
		{
			name:  "kitty ctrl-w translates to legacy control byte",
			input: []byte("\x1b[119;5u"),
			want:  []byte{0x17},
		},
		{
			name:  "kitty ctrl-shift-a translates to legacy control byte",
			input: []byte("\x1b[97;6;65u"),
			want:  []byte{0x01},
		},
		{
			name:  "kitty ctrl-9 translates to legacy printable byte",
			input: []byte("\x1b[57;5u"),
			want:  []byte("9"),
		},
		{
			name:  "kitty ctrl-slash translates to legacy control byte",
			input: []byte("\x1b[47;5u"),
			want:  []byte{0x1f},
		},
		{
			name:  "kitty alt-h translates to legacy esc-prefixed bytes",
			input: []byte("\x1b[104;3u"),
			want:  []byte{0x1b, 'h'},
		},
		{
			name:  "kitty alt-shift-a translates to legacy esc-prefixed bytes",
			input: []byte("\x1b[97;4;65u"),
			want:  []byte{0x1b, 'A'},
		},
		{
			name:  "kitty alt-up translates to legacy esc-prefixed special-key bytes",
			input: []byte("\x1b[1;3A"),
			want:  []byte{0x1b, 0x1b, '[', 'A'},
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

			if got := forwardedBytesForDecodedInput(decodeSingleInputEvent(t, tt.input)); !bytes.Equal(got, tt.want) {
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

func TestForwardedBytesForDecodedInputHandlesEmptyRaw(t *testing.T) {
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
