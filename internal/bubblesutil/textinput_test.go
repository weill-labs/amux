package bubblesutil

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTextInputStateUpdate(t *testing.T) {
	t.Parallel()

	state := TextInputState{Value: "logs", Cursor: 4}
	state.Update(tea.KeyMsg{Type: tea.KeyLeft})
	state.Update(tea.KeyMsg{Type: tea.KeyLeft})
	state.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	if state.Value != "lo" || state.Cursor != 2 {
		t.Fatalf("after ctrl-k state = %+v, want Value=lo Cursor=2", state)
	}

	state.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	state.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if state.Value != "xlo" || state.Cursor != 1 {
		t.Fatalf("after insertion state = %+v, want Value=xlo Cursor=1", state)
	}
}

func TestTextInputKeyHelpers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		key            tea.KeyType
		wantInlineEdit bool
		wantTextInput  bool
	}{
		{name: "left", key: tea.KeyLeft, wantInlineEdit: true, wantTextInput: true},
		{name: "ctrl-k", key: tea.KeyCtrlK, wantInlineEdit: true, wantTextInput: true},
		{name: "runes", key: tea.KeyRunes, wantTextInput: true},
		{name: "home", key: tea.KeyHome, wantTextInput: true},
		{name: "enter", key: tea.KeyEnter},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsInlineEditingKey(tt.key); got != tt.wantInlineEdit {
				t.Fatalf("IsInlineEditingKey(%v) = %v, want %v", tt.key, got, tt.wantInlineEdit)
			}
			if got := IsTextInputKey(tt.key); got != tt.wantTextInput {
				t.Fatalf("IsTextInputKey(%v) = %v, want %v", tt.key, got, tt.wantTextInput)
			}
		})
	}
}

func TestDecodeKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        []byte
		wantType     tea.KeyType
		wantRunes    string
		wantAlt      bool
		wantConsumed int
		wantOK       bool
	}{
		{name: "empty", input: nil, wantConsumed: 0},
		{name: "printable rune", input: []byte("a"), wantType: tea.KeyRunes, wantRunes: "a", wantConsumed: 1, wantOK: true},
		{name: "enter", input: []byte{'\r'}, wantType: tea.KeyEnter, wantConsumed: 1, wantOK: true},
		{name: "tab", input: []byte{'\t'}, wantType: tea.KeyTab, wantConsumed: 1, wantOK: true},
		{name: "backspace", input: []byte{0x7f}, wantType: tea.KeyBackspace, wantConsumed: 1, wantOK: true},
		{name: "ctrl-a", input: []byte{0x01}, wantType: tea.KeyCtrlA, wantConsumed: 1, wantOK: true},
		{name: "ctrl-b", input: []byte{0x02}, wantType: tea.KeyCtrlB, wantConsumed: 1, wantOK: true},
		{name: "ctrl-d", input: []byte{0x04}, wantType: tea.KeyCtrlD, wantConsumed: 1, wantOK: true},
		{name: "ctrl-e", input: []byte{0x05}, wantType: tea.KeyCtrlE, wantConsumed: 1, wantOK: true},
		{name: "ctrl-f", input: []byte{0x06}, wantType: tea.KeyCtrlF, wantConsumed: 1, wantOK: true},
		{name: "ctrl-k", input: []byte{0x0b}, wantType: tea.KeyCtrlK, wantConsumed: 1, wantOK: true},
		{name: "ctrl-n", input: []byte{0x0e}, wantType: tea.KeyCtrlN, wantConsumed: 1, wantOK: true},
		{name: "ctrl-p", input: []byte{0x10}, wantType: tea.KeyCtrlP, wantConsumed: 1, wantOK: true},
		{name: "ctrl-u", input: []byte{0x15}, wantType: tea.KeyCtrlU, wantConsumed: 1, wantOK: true},
		{name: "ctrl-w", input: []byte{0x17}, wantType: tea.KeyCtrlW, wantConsumed: 1, wantOK: true},
		{name: "unsupported control", input: []byte{0x03}, wantConsumed: 1},
		{name: "escape", input: []byte{0x1b}, wantType: tea.KeyEsc, wantConsumed: 1, wantOK: true},
		{name: "alt printable", input: []byte{0x1b, 'b'}, wantType: tea.KeyRunes, wantRunes: "b", wantAlt: true, wantConsumed: 2, wantOK: true},
		{name: "alt control falls back to escape", input: []byte{0x1b, 0x01}, wantType: tea.KeyEsc, wantConsumed: 1, wantOK: true},
		{name: "ss3 home", input: []byte{0x1b, 'O', 'H'}, wantType: tea.KeyHome, wantConsumed: 3, wantOK: true},
		{name: "ss3 end", input: []byte{0x1b, 'O', 'F'}, wantType: tea.KeyEnd, wantConsumed: 3, wantOK: true},
		{name: "ss3 short", input: []byte{0x1b, 'O'}, wantType: tea.KeyEsc, wantConsumed: 1, wantOK: true},
		{name: "ss3 unknown", input: []byte{0x1b, 'O', 'Q'}, wantType: tea.KeyEsc, wantConsumed: 1, wantOK: true},
		{name: "csi short", input: []byte{0x1b, '['}, wantType: tea.KeyEsc, wantConsumed: 1, wantOK: true},
		{name: "up", input: []byte{0x1b, '[', 'A'}, wantType: tea.KeyUp, wantConsumed: 3, wantOK: true},
		{name: "down", input: []byte{0x1b, '[', 'B'}, wantType: tea.KeyDown, wantConsumed: 3, wantOK: true},
		{name: "right", input: []byte{0x1b, '[', 'C'}, wantType: tea.KeyRight, wantConsumed: 3, wantOK: true},
		{name: "left", input: []byte{0x1b, '[', 'D'}, wantType: tea.KeyLeft, wantConsumed: 3, wantOK: true},
		{name: "home", input: []byte{0x1b, '[', 'H'}, wantType: tea.KeyHome, wantConsumed: 3, wantOK: true},
		{name: "end", input: []byte{0x1b, '[', 'F'}, wantType: tea.KeyEnd, wantConsumed: 3, wantOK: true},
		{name: "delete", input: []byte{0x1b, '[', '3', '~'}, wantType: tea.KeyDelete, wantConsumed: 4, wantOK: true},
		{name: "page up", input: []byte{0x1b, '[', '5', '~'}, wantType: tea.KeyPgUp, wantConsumed: 4, wantOK: true},
		{name: "page down", input: []byte{0x1b, '[', '6', '~'}, wantType: tea.KeyPgDown, wantConsumed: 4, wantOK: true},
		{name: "ctrl up", input: []byte{0x1b, '[', '1', ';', '5', 'A'}, wantType: tea.KeyCtrlUp, wantConsumed: 6, wantOK: true},
		{name: "ctrl down", input: []byte{0x1b, '[', '1', ';', '5', 'B'}, wantType: tea.KeyCtrlDown, wantConsumed: 6, wantOK: true},
		{name: "alt right", input: []byte{0x1b, '[', '1', ';', '3', 'C'}, wantType: tea.KeyRight, wantAlt: true, wantConsumed: 6, wantOK: true},
		{name: "alt left", input: []byte{0x1b, '[', '1', ';', '3', 'D'}, wantType: tea.KeyLeft, wantAlt: true, wantConsumed: 6, wantOK: true},
		{name: "truncated params", input: []byte{0x1b, '[', '1', ';'}, wantType: tea.KeyEsc, wantConsumed: 1, wantOK: true},
		{name: "unsupported csi", input: []byte{0x1b, '[', '1', ';', '2', 'A'}, wantType: tea.KeyEsc, wantConsumed: 1, wantOK: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, consumed, ok := DecodeKey(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("DecodeKey(%v) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if consumed != tt.wantConsumed {
				t.Fatalf("DecodeKey(%v) consumed = %d, want %d", tt.input, consumed, tt.wantConsumed)
			}
			if got.Type != tt.wantType {
				t.Fatalf("DecodeKey(%v) type = %v, want %v", tt.input, got.Type, tt.wantType)
			}
			if string(got.Runes) != tt.wantRunes {
				t.Fatalf("DecodeKey(%v) runes = %q, want %q", tt.input, string(got.Runes), tt.wantRunes)
			}
			if got.Alt != tt.wantAlt {
				t.Fatalf("DecodeKey(%v) alt = %v, want %v", tt.input, got.Alt, tt.wantAlt)
			}
		})
	}
}
