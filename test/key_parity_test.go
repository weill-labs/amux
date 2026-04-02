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
		{
			name:  "ctrl space alias",
			token: "C-Space",
			want:  []byte{0x00},
		},
		{
			name:  "ctrl digit alias",
			token: "C-3",
			want:  []byte{0x1b},
		},
		{
			name:  "ctrl slash alias",
			token: "C-/",
			want:  []byte{0x1f},
		},
		{
			name:  "meta printable",
			token: "M-a",
			want:  []byte{0x1b, 'a'},
		},
		{
			name:  "meta arrow",
			token: "M-Up",
			want:  []byte{0x1b, 0x1b, '[', 'A'},
		},
		{
			name:  "meta enter",
			token: "M-Enter",
			want:  []byte{0x1b, '\r'},
		},
		{
			name:  "escape alias",
			token: "Esc",
			want:  []byte{0x1b},
		},
		{
			name:  "backspace alias",
			token: "Backspace",
			want:  []byte{0x7f},
		},
		{
			name:  "page up alias",
			token: "PgUp",
			want:  []byte{0x1b, '[', '5', '~'},
		},
		{
			name:  "backtab alias",
			token: "BTab",
			want:  []byte{0x1b, '[', 'Z'},
		},
		{
			name:  "function key f1",
			token: "F1",
			want:  []byte{0x1b, 'O', 'P'},
		},
		{
			name:  "function key f12",
			token: "F12",
			want:  []byte{0x1b, '[', '2', '4', '~'},
		},
		{
			name:  "keypad digit",
			token: "KP0",
			want:  []byte{0x1b, 'O', 'p'},
		},
		{
			name:  "keypad enter",
			token: "KPEnter",
			want:  []byte{0x1b, 'O', 'M'},
		},
		{
			name:  "keypad multiply",
			token: "KPMultiply",
			want:  []byte{0x1b, 'O', 'j'},
		},
		{
			name:  "keypad period alias",
			token: "KPPeriod",
			want:  []byte{0x1b, 'O', 'n'},
		},
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
