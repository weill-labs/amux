package test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/render"
)

func rawReadCommand(byteCount int) string {
	return fmt.Sprintf(`old=$(stty -g); stty raw -echo; printf 'READY\n'; dd bs=1 count=%d 2>/dev/null | od -An -tx1 | tr -d ' \n'; printf '\n'; stty "$old"`, byteCount)
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
			h.waitFor("pane-1", "READY")

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

func TestPTYClientKittyKeyboardPrintableCtrlFallbacks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     []byte
		readBytes int
		wantHex   string
	}{
		{
			name:      "ctrl-9 becomes printable 9",
			input:     []byte("\x1b[57;5u"),
			readBytes: 1,
			wantHex:   "39",
		},
		{
			name:      "ctrl-3 becomes escape",
			input:     []byte("\x1b[51;5u"),
			readBytes: 1,
			wantHex:   "1b",
		},
		{
			name:      "ctrl-slash becomes unit separator",
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
			h.waitFor("pane-1", "READY")

			client.write(tt.input)
			h.waitForTimeout("pane-1", tt.wantHex, "5s")
		})
	}
}
