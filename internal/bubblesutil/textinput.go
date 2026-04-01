package bubblesutil

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// TextInputState is the persisted subset of a Bubbles textinput model that we
// need across atomic client snapshots and copy-mode prompt updates.
type TextInputState struct {
	Value  string
	Cursor int
}

func (s TextInputState) Model() textinput.Model {
	model := textinput.New()
	model.Prompt = "> "
	model.SetValue(s.Value)
	model.SetCursor(s.Cursor)
	model.Focus()
	return model
}

func (s *TextInputState) Update(msg tea.KeyMsg) {
	model := s.Model()
	model, _ = model.Update(msg)
	s.Value = model.Value()
	s.Cursor = model.Position()
}

// DecodeKey translates normalized legacy terminal bytes into a Bubble Tea key
// message. The caller is expected to pass input after any kitty-keyboard
// normalization.
func DecodeKey(data []byte) (tea.KeyMsg, int, bool) {
	if len(data) == 0 {
		return tea.KeyMsg{}, 0, false
	}
	if data[0] != 0x1b {
		msg, ok := decodeControlOrRune(data[0])
		return msg, 1, ok
	}
	if len(data) == 1 {
		return tea.KeyMsg{Type: tea.KeyEsc}, 1, true
	}

	if data[1] != '[' && data[1] != 'O' {
		if data[1] >= 0x20 && data[1] <= 0x7e {
			return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune(data[1])}, Alt: true}, 2, true
		}
		return tea.KeyMsg{Type: tea.KeyEsc}, 1, true
	}

	if data[1] == 'O' {
		if len(data) < 3 {
			return tea.KeyMsg{Type: tea.KeyEsc}, 1, true
		}
		switch data[2] {
		case 'H':
			return tea.KeyMsg{Type: tea.KeyHome}, 3, true
		case 'F':
			return tea.KeyMsg{Type: tea.KeyEnd}, 3, true
		default:
			return tea.KeyMsg{Type: tea.KeyEsc}, 1, true
		}
	}

	if len(data) < 3 {
		return tea.KeyMsg{Type: tea.KeyEsc}, 1, true
	}
	switch data[2] {
	case 'A':
		return tea.KeyMsg{Type: tea.KeyUp}, 3, true
	case 'B':
		return tea.KeyMsg{Type: tea.KeyDown}, 3, true
	case 'C':
		return tea.KeyMsg{Type: tea.KeyRight}, 3, true
	case 'D':
		return tea.KeyMsg{Type: tea.KeyLeft}, 3, true
	case 'H':
		return tea.KeyMsg{Type: tea.KeyHome}, 3, true
	case 'F':
		return tea.KeyMsg{Type: tea.KeyEnd}, 3, true
	}

	end := 2
	for end < len(data) && ((data[end] >= '0' && data[end] <= '9') || data[end] == ';') {
		end++
	}
	if end >= len(data) {
		return tea.KeyMsg{Type: tea.KeyEsc}, 1, true
	}

	params := string(data[2:end])
	final := data[end]
	switch {
	case final == '~' && params == "3":
		return tea.KeyMsg{Type: tea.KeyDelete}, end + 1, true
	case final == '~' && params == "5":
		return tea.KeyMsg{Type: tea.KeyPgUp}, end + 1, true
	case final == '~' && params == "6":
		return tea.KeyMsg{Type: tea.KeyPgDown}, end + 1, true
	case final == 'A' && (params == "1;5" || params == "5"):
		return tea.KeyMsg{Type: tea.KeyCtrlUp}, end + 1, true
	case final == 'B' && (params == "1;5" || params == "5"):
		return tea.KeyMsg{Type: tea.KeyCtrlDown}, end + 1, true
	case final == 'C' && params == "1;3":
		return tea.KeyMsg{Type: tea.KeyRight, Alt: true}, end + 1, true
	case final == 'D' && params == "1;3":
		return tea.KeyMsg{Type: tea.KeyLeft, Alt: true}, end + 1, true
	default:
		return tea.KeyMsg{Type: tea.KeyEsc}, 1, true
	}
}

func decodeControlOrRune(b byte) (tea.KeyMsg, bool) {
	switch b {
	case '\r', '\n':
		return tea.KeyMsg{Type: tea.KeyEnter}, true
	case '\t':
		return tea.KeyMsg{Type: tea.KeyTab}, true
	case 0x7f, 0x08:
		return tea.KeyMsg{Type: tea.KeyBackspace}, true
	case 0x01:
		return tea.KeyMsg{Type: tea.KeyCtrlA}, true
	case 0x02:
		return tea.KeyMsg{Type: tea.KeyCtrlB}, true
	case 0x04:
		return tea.KeyMsg{Type: tea.KeyCtrlD}, true
	case 0x05:
		return tea.KeyMsg{Type: tea.KeyCtrlE}, true
	case 0x06:
		return tea.KeyMsg{Type: tea.KeyCtrlF}, true
	case 0x0b:
		return tea.KeyMsg{Type: tea.KeyCtrlK}, true
	case 0x0e:
		return tea.KeyMsg{Type: tea.KeyCtrlN}, true
	case 0x10:
		return tea.KeyMsg{Type: tea.KeyCtrlP}, true
	case 0x15:
		return tea.KeyMsg{Type: tea.KeyCtrlU}, true
	case 0x17:
		return tea.KeyMsg{Type: tea.KeyCtrlW}, true
	default:
		if b >= 0x20 && b <= 0x7e {
			return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune(b)}}, true
		}
		return tea.KeyMsg{}, false
	}
}
