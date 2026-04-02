package vt

import (
	"fmt"
	"io"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

const terminalIOTimeout = time.Second

type readResult struct {
	data string
	err  error
}

func readTerminalInput(t testing.TB, term *Emulator, wantBytes int) string {
	t.Helper()

	resultCh := make(chan readResult, 1)
	go func() {
		buf := make([]byte, wantBytes)
		n, err := io.ReadFull(term, buf)
		resultCh <- readResult{data: string(buf[:n]), err: err}
	}()

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("Read() error = %v", result.err)
		}
		return result.data
	case <-time.After(terminalIOTimeout):
		_ = term.Close()
		result := <-resultCh
		t.Fatalf("timed out reading %d bytes; got %q, err=%v", wantBytes, result.data, result.err)
		return ""
	}
}

func writeStringAndReadTerminalInput(t testing.TB, term *Emulator, input string, wantBytes int) string {
	t.Helper()

	errCh := make(chan error, 1)
	go func() {
		_, err := term.WriteString(input)
		errCh <- err
	}()

	got := readTerminalInput(t, term, wantBytes)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("WriteString(%q) error = %v", input, err)
		}
	case <-time.After(terminalIOTimeout):
		_ = term.Close()
		t.Fatalf("timed out waiting for WriteString(%q)", input)
	}

	return got
}

func sendKeyAndReadTerminalInput(t testing.TB, term *Emulator, key uv.KeyEvent, wantBytes int) string {
	t.Helper()

	done := make(chan struct{}, 1)
	go func() {
		term.SendKey(key)
		done <- struct{}{}
	}()

	got := readTerminalInput(t, term, wantBytes)

	select {
	case <-done:
	case <-time.After(terminalIOTimeout):
		_ = term.Close()
		t.Fatalf("timed out waiting for SendKey(%v)", key)
	}

	return got
}

func kittyKeyboardReport(flags int) string {
	return fmt.Sprintf("\x1b[?%du", flags)
}

func TestKittyKeyboardPushPopAndQuery(t *testing.T) {
	t.Parallel()

	term := NewEmulator(80, 24)

	if got := writeStringAndReadTerminalInput(t, term, ansi.RequestKittyKeyboard, len(kittyKeyboardReport(0))); got != kittyKeyboardReport(0) {
		t.Fatalf("initial query = %q, want %q", got, kittyKeyboardReport(0))
	}

	if _, err := term.WriteString(ansi.PushKittyKeyboard(1)); err != nil {
		t.Fatalf("WriteString(push 1) error = %v", err)
	}
	if got := writeStringAndReadTerminalInput(t, term, ansi.RequestKittyKeyboard, len(kittyKeyboardReport(1))); got != kittyKeyboardReport(1) {
		t.Fatalf("query after push 1 = %q, want %q", got, kittyKeyboardReport(1))
	}

	if _, err := term.WriteString(ansi.PushKittyKeyboard(3)); err != nil {
		t.Fatalf("WriteString(push 3) error = %v", err)
	}
	if got := writeStringAndReadTerminalInput(t, term, ansi.RequestKittyKeyboard, len(kittyKeyboardReport(3))); got != kittyKeyboardReport(3) {
		t.Fatalf("query after push 3 = %q, want %q", got, kittyKeyboardReport(3))
	}

	if _, err := term.WriteString(ansi.PopKittyKeyboard(0)); err != nil {
		t.Fatalf("WriteString(pop default) error = %v", err)
	}
	if got := writeStringAndReadTerminalInput(t, term, ansi.RequestKittyKeyboard, len(kittyKeyboardReport(1))); got != kittyKeyboardReport(1) {
		t.Fatalf("query after pop default = %q, want %q", got, kittyKeyboardReport(1))
	}

	if _, err := term.WriteString(ansi.PopKittyKeyboard(2)); err != nil {
		t.Fatalf("WriteString(pop 2) error = %v", err)
	}
	if got := writeStringAndReadTerminalInput(t, term, ansi.RequestKittyKeyboard, len(kittyKeyboardReport(0))); got != kittyKeyboardReport(0) {
		t.Fatalf("query after pop 2 = %q, want %q", got, kittyKeyboardReport(0))
	}
}

func TestKittyKeyboardStackIsScreenLocal(t *testing.T) {
	t.Parallel()

	term := NewEmulator(80, 24)

	if _, err := term.WriteString(ansi.PushKittyKeyboard(1)); err != nil {
		t.Fatalf("WriteString(push 1) error = %v", err)
	}
	if got := writeStringAndReadTerminalInput(t, term, ansi.RequestKittyKeyboard, len(kittyKeyboardReport(1))); got != kittyKeyboardReport(1) {
		t.Fatalf("main screen query = %q, want %q", got, kittyKeyboardReport(1))
	}

	if _, err := term.WriteString("\x1b[?1049h"); err != nil {
		t.Fatalf("WriteString(enter alt screen) error = %v", err)
	}
	if got := writeStringAndReadTerminalInput(t, term, ansi.RequestKittyKeyboard, len(kittyKeyboardReport(0))); got != kittyKeyboardReport(0) {
		t.Fatalf("alt screen initial query = %q, want %q", got, kittyKeyboardReport(0))
	}

	if _, err := term.WriteString(ansi.PushKittyKeyboard(2)); err != nil {
		t.Fatalf("WriteString(push 2 in alt screen) error = %v", err)
	}
	if got := writeStringAndReadTerminalInput(t, term, ansi.RequestKittyKeyboard, len(kittyKeyboardReport(2))); got != kittyKeyboardReport(2) {
		t.Fatalf("alt screen query after push = %q, want %q", got, kittyKeyboardReport(2))
	}

	if _, err := term.WriteString("\x1b[?1049l"); err != nil {
		t.Fatalf("WriteString(leave alt screen) error = %v", err)
	}
	if got := writeStringAndReadTerminalInput(t, term, ansi.RequestKittyKeyboard, len(kittyKeyboardReport(1))); got != kittyKeyboardReport(1) {
		t.Fatalf("main screen query after returning = %q, want %q", got, kittyKeyboardReport(1))
	}
}

func TestSendKeyUsesKittyKeyboardEnhancements(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		enable     string
		key        uv.KeyEvent
		wantOutput string
	}{
		{
			name:       "ctrl a uses kitty csi u",
			enable:     ansi.PushKittyKeyboard(ansi.KittyDisambiguateEscapeCodes),
			key:        uv.KeyPressEvent{Code: 'a', Mod: uv.ModCtrl},
			wantOutput: "\x1b[97;5u",
		},
		{
			name:       "escape uses kitty csi u",
			enable:     ansi.PushKittyKeyboard(ansi.KittyDisambiguateEscapeCodes),
			key:        uv.KeyPressEvent{Code: uv.KeyEscape},
			wantOutput: "\x1b[27u",
		},
		{
			name:       "up arrow uses kitty functional code",
			enable:     ansi.PushKittyKeyboard(ansi.KittyDisambiguateEscapeCodes),
			key:        uv.KeyPressEvent{Code: uv.KeyUp},
			wantOutput: "\x1b[57352u",
		},
		{
			name:       "plain text stays utf8 without report all keys",
			enable:     ansi.PushKittyKeyboard(ansi.KittyDisambiguateEscapeCodes),
			key:        uv.KeyPressEvent{Code: 'a', Text: "a"},
			wantOutput: "a",
		},
		{
			name:       "plain text uses kitty csi u with report all keys",
			enable:     ansi.PushKittyKeyboard(ansi.KittyReportAllKeysAsEscapeCodes),
			key:        uv.KeyPressEvent{Code: 'a', Text: "a"},
			wantOutput: "\x1b[97u",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			term := NewEmulator(80, 24)
			if _, err := term.WriteString(tt.enable); err != nil {
				t.Fatalf("WriteString(%q) error = %v", tt.enable, err)
			}

			if got := sendKeyAndReadTerminalInput(t, term, tt.key, len(tt.wantOutput)); got != tt.wantOutput {
				t.Fatalf("SendKey(%v) = %q, want %q", tt.key, got, tt.wantOutput)
			}
		})
	}
}
