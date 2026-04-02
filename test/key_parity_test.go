package test

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"
)

const rawReadDoneMarker = "__RAW_DONE__"

func rawReadCommandWithDeadline(byteCount int, timeout time.Duration) string {
	return fmt.Sprintf(`python3 -u -c 'import os,select,sys,termios,time,tty
fd=sys.stdin.fileno()
old=termios.tcgetattr(fd)
ready="".join(chr(c) for c in [95,95,82,65,87,95,82,69,65,68,89,95,95])
done="".join(chr(c) for c in [95,95,82,65,87,95,68,79,78,69,95,95])
tty.setraw(fd)
try:
    while True:
        r, _, _ = select.select([fd], [], [], 0)
        if not r:
            break
        if not os.read(fd, 4096):
            break
    print(ready, flush=True)
    deadline=time.monotonic()+%0.3f
    data=bytearray()
    while len(data) < %d and time.monotonic() < deadline:
        wait=max(0, deadline-time.monotonic())
        r, _, _ = select.select([fd], [], [], wait)
        if not r:
            break
        chunk=os.read(fd, %d-len(data))
        if not chunk:
            break
        data.extend(chunk)
    print("HEX="+data.hex(), flush=True)
    print(done, flush=True)
finally:
    termios.tcsetattr(fd, termios.TCSADRAIN, old)'`, timeout.Seconds(), byteCount, byteCount)
}

func TestSendKeysEncodeParityMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token string
		want  []byte
	}{
		{name: "ctrl a", token: "C-a", want: []byte{0x01}},
		{name: "ctrl z", token: "C-z", want: []byte{0x1a}},
		{name: "ctrl space alias", token: "C-Space", want: []byte{0x00}},
		{name: "ctrl 2 alias", token: "C-2", want: []byte{0x00}},
		{name: "ctrl digit alias", token: "C-3", want: []byte{0x1b}},
		{name: "ctrl 4 alias", token: "C-4", want: []byte{0x1c}},
		{name: "ctrl 5 alias", token: "C-5", want: []byte{0x1d}},
		{name: "ctrl 6 alias", token: "C-6", want: []byte{0x1e}},
		{name: "ctrl 7 alias", token: "C-7", want: []byte{0x1f}},
		{name: "ctrl 8 alias", token: "C-8", want: []byte{0x7f}},
		{name: "ctrl 9 alias", token: "C-9", want: []byte{'9'}},
		{name: "ctrl slash alias", token: "C-/", want: []byte{0x1f}},

		{name: "meta printable", token: "M-a", want: []byte{0x1b, 'a'}},
		{name: "meta shifted printable", token: "M-A", want: []byte{0x1b, 'A'}},
		{name: "meta arrow", token: "M-Up", want: []byte{0x1b, 0x1b, '[', 'A'}},
		{name: "meta enter", token: "M-Enter", want: []byte{0x1b, '\r'}},
		{name: "meta backspace", token: "M-BSpace", want: []byte{0x1b, 0x7f}},

		{name: "enter canonical", token: "Enter", want: []byte{'\r'}},
		{name: "return alias", token: "Return", want: []byte{'\r'}},
		{name: "tab canonical", token: "Tab", want: []byte{'\t'}},
		{name: "backtab alias", token: "BTab", want: []byte{0x1b, '[', 'Z'}},
		{name: "escape canonical", token: "Escape", want: []byte{0x1b}},
		{name: "escape alias", token: "Esc", want: []byte{0x1b}},
		{name: "backspace canonical", token: "BSpace", want: []byte{0x7f}},
		{name: "backspace alias", token: "Backspace", want: []byte{0x7f}},
		{name: "up arrow", token: "Up", want: []byte{0x1b, '[', 'A'}},
		{name: "down arrow", token: "Down", want: []byte{0x1b, '[', 'B'}},
		{name: "right arrow", token: "Right", want: []byte{0x1b, '[', 'C'}},
		{name: "left arrow", token: "Left", want: []byte{0x1b, '[', 'D'}},
		{name: "home key", token: "Home", want: []byte{0x1b, '[', 'H'}},
		{name: "end key", token: "End", want: []byte{0x1b, '[', 'F'}},
		{name: "page up canonical", token: "PageUp", want: []byte{0x1b, '[', '5', '~'}},
		{name: "page up alias", token: "PgUp", want: []byte{0x1b, '[', '5', '~'}},
		{name: "page down canonical", token: "PageDown", want: []byte{0x1b, '[', '6', '~'}},
		{name: "page down alias", token: "PgDn", want: []byte{0x1b, '[', '6', '~'}},
		{name: "insert key", token: "Insert", want: []byte{0x1b, '[', '2', '~'}},
		{name: "delete key", token: "Delete", want: []byte{0x1b, '[', '3', '~'}},

		{name: "function key f1", token: "F1", want: []byte{0x1b, 'O', 'P'}},
		{name: "function key f2", token: "F2", want: []byte{0x1b, 'O', 'Q'}},
		{name: "function key f3", token: "F3", want: []byte{0x1b, 'O', 'R'}},
		{name: "function key f4", token: "F4", want: []byte{0x1b, 'O', 'S'}},
		{name: "function key f5", token: "F5", want: []byte{0x1b, '[', '1', '5', '~'}},
		{name: "function key f6", token: "F6", want: []byte{0x1b, '[', '1', '7', '~'}},
		{name: "function key f7", token: "F7", want: []byte{0x1b, '[', '1', '8', '~'}},
		{name: "function key f8", token: "F8", want: []byte{0x1b, '[', '1', '9', '~'}},
		{name: "function key f9", token: "F9", want: []byte{0x1b, '[', '2', '0', '~'}},
		{name: "function key f10", token: "F10", want: []byte{0x1b, '[', '2', '1', '~'}},
		{name: "function key f11", token: "F11", want: []byte{0x1b, '[', '2', '3', '~'}},
		{name: "function key f12", token: "F12", want: []byte{0x1b, '[', '2', '4', '~'}},

		{name: "keypad digit 0", token: "KP0", want: []byte{0x1b, 'O', 'p'}},
		{name: "keypad digit 1", token: "KP1", want: []byte{0x1b, 'O', 'q'}},
		{name: "keypad digit 2", token: "KP2", want: []byte{0x1b, 'O', 'r'}},
		{name: "keypad digit 3", token: "KP3", want: []byte{0x1b, 'O', 's'}},
		{name: "keypad digit 4", token: "KP4", want: []byte{0x1b, 'O', 't'}},
		{name: "keypad digit 5", token: "KP5", want: []byte{0x1b, 'O', 'u'}},
		{name: "keypad digit 6", token: "KP6", want: []byte{0x1b, 'O', 'v'}},
		{name: "keypad digit 7", token: "KP7", want: []byte{0x1b, 'O', 'w'}},
		{name: "keypad digit 8", token: "KP8", want: []byte{0x1b, 'O', 'x'}},
		{name: "keypad digit 9", token: "KP9", want: []byte{0x1b, 'O', 'y'}},
		{name: "keypad enter", token: "KPEnter", want: []byte{0x1b, 'O', 'M'}},
		{name: "keypad equal", token: "KPEqual", want: []byte{0x1b, 'O', 'X'}},
		{name: "keypad multiply", token: "KPMultiply", want: []byte{0x1b, 'O', 'j'}},
		{name: "keypad plus", token: "KPPlus", want: []byte{0x1b, 'O', 'k'}},
		{name: "keypad comma", token: "KPComma", want: []byte{0x1b, 'O', 'l'}},
		{name: "keypad minus", token: "KPMinus", want: []byte{0x1b, 'O', 'm'}},
		{name: "keypad decimal", token: "KPDecimal", want: []byte{0x1b, 'O', 'n'}},
		{name: "keypad period alias", token: "KPPeriod", want: []byte{0x1b, 'O', 'n'}},
		{name: "keypad divide", token: "KPDivide", want: []byte{0x1b, 'O', 'o'}},
	}

	h := newServerHarness(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			readBytes := len(tt.want)
			if len(tt.token) > readBytes {
				readBytes = len(tt.token)
			}

			h.sendKeys("pane-1", rawReadCommandWithDeadline(readBytes, 250*time.Millisecond), "Enter")
			h.waitFor("pane-1", rawReadReadyMarker)

			out := h.runCmd("send-keys", "pane-1", tt.token)
			if strings.Contains(out, "invalid") {
				t.Fatalf("send-keys %q failed: %s", tt.token, out)
			}

			h.waitFor("pane-1", rawReadDoneMarker)

			wantHex := hex.EncodeToString(tt.want)
			pane := h.runCmd("capture", "pane-1")
			if !strings.Contains(pane, "HEX="+wantHex) {
				t.Fatalf("send-keys %q hex output missing %q\nPane:\n%s", tt.token, "HEX="+wantHex, pane)
			}
		})
	}
}
