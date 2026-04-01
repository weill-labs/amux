package test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/render"
)

const rawReadReadyMarker = "__RAW_READY__"

func rawReadCommand(byteCount int) string {
	return fmt.Sprintf(`python3 -u -c 'import os,sys,tty,termios; fd=sys.stdin.fileno(); old=termios.tcgetattr(fd); marker="".join(chr(c) for c in [95,95,82,65,87,95,82,69,65,68,89,95,95]); tty.setraw(fd); print(marker, flush=True); data=os.read(fd, %d); print(data.hex(), flush=True); termios.tcsetattr(fd, termios.TCSADRAIN, old)'`, byteCount)
}

func TestPTYClientKittyKeyboardChangesPaneBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		env       string
		wantKitty bool
		readBytes int
		wantHex   string
	}{
		{
			name:      "legacy",
			env:       "AMUX_CLIENT_CAPABILITIES=legacy",
			wantKitty: false,
			readBytes: 1,
			wantHex:   "02",
		},
		{
			name:      "kitty keyboard",
			env:       "AMUX_CLIENT_CAPABILITIES=kitty_keyboard",
			wantKitty: true,
			readBytes: 1,
			wantHex:   "02",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newServerHarness(t)
			client := newPTYClientHarness(t, h, tt.env)

			if got := client.kittyKeyboardEnabled(); got != tt.wantKitty {
				t.Fatalf("kittyKeyboardEnabled() = %v, want %v\nOutput:\n%s", got, tt.wantKitty, client.outputString())
			}

			h.sendKeys("pane-1", rawReadCommand(tt.readBytes), "Enter")
			h.waitFor("pane-1", rawReadReadyMarker)

			client.sendCtrl('b')
			h.waitForTimeout("pane-1", tt.wantHex, "5s")

			client.detach()
			if !client.waitExited(5 * time.Second) {
				t.Fatalf("PTY client did not exit after detach\nOutput:\n%s", client.outputString())
			}
			if err := client.waitError(); err != nil {
				t.Fatalf("PTY client exited with error: %v\nOutput:\n%s", err, client.outputString())
			}

			if tt.wantKitty {
				if !client.waitForOutput(render.KittyKeyboardDisable, 2*time.Second) {
					t.Fatalf("expected kitty disable sequence on exit\nOutput:\n%s", client.outputString())
				}
				return
			}
			if strings.Contains(client.outputString(), render.KittyKeyboardDisable) {
				t.Fatalf("legacy client should not emit kitty disable\nOutput:\n%s", client.outputString())
			}
		})
	}
}

func TestPTYClientKittyKeyboardCtrlSequencesUseLegacyPaneBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     []byte
		readBytes int
		wantHex   string
	}{
		{
			name:      "ctrl-c translates to legacy control byte",
			input:     []byte("\x1b[99;5u"),
			readBytes: 1,
			wantHex:   "03",
		},
		{
			name:      "ctrl-r translates to legacy control byte",
			input:     []byte("\x1b[114;5u"),
			readBytes: 1,
			wantHex:   "12",
		},
		{
			name:      "ctrl-d translates to legacy control byte",
			input:     []byte("\x1b[100;5u"),
			readBytes: 1,
			wantHex:   "04",
		},
		{
			name:      "ctrl-z translates to legacy control byte",
			input:     []byte("\x1b[122;5u"),
			readBytes: 1,
			wantHex:   "1a",
		},
		{
			name:      "ctrl-l translates to legacy control byte",
			input:     []byte("\x1b[108;5u"),
			readBytes: 1,
			wantHex:   "0c",
		},
		{
			name:      "ctrl-w translates to legacy control byte",
			input:     []byte("\x1b[119;5u"),
			readBytes: 1,
			wantHex:   "17",
		},
		{
			name:      "ctrl-9 translates to legacy printable byte",
			input:     []byte("\x1b[57;5u"),
			readBytes: 1,
			wantHex:   "39",
		},
		{
			name:      "ctrl-3 translates to legacy escape byte",
			input:     []byte("\x1b[51;5u"),
			readBytes: 1,
			wantHex:   "1b",
		},
		{
			name:      "ctrl-slash translates to legacy control byte",
			input:     []byte("\x1b[47;5u"),
			readBytes: 1,
			wantHex:   "1f",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newServerHarness(t)
			client := newPTYClientHarness(t, h, "AMUX_CLIENT_CAPABILITIES=kitty_keyboard")

			if !client.kittyKeyboardEnabled() {
				t.Fatalf("expected kitty keyboard enable sequence\nOutput:\n%s", client.outputString())
			}

			h.sendKeys("pane-1", rawReadCommand(tt.readBytes), "Enter")
			h.waitFor("pane-1", rawReadReadyMarker)

			client.write(tt.input)
			h.waitForTimeout("pane-1", tt.wantHex, "5s")
		})
	}
}
