package test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPTYClientTextInputEchoesInPane(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	client := newPTYClientHarness(t, h)

	token := fmt.Sprintf("ci-%d", time.Now().UnixNano()%1_000_000)
	client.sendText(token)

	if !client.waitForOutput(token, 5*time.Second) {
		t.Fatalf("typed text %q did not echo in client output\nOutput:\n%s", token, client.outputString())
	}

	client.detach()
	if !client.waitExited(5 * time.Second) {
		t.Fatalf("PTY client did not exit after detach\nOutput:\n%s", client.outputString())
	}
	if err := client.waitError(); err != nil {
		t.Fatalf("PTY client exited with error: %v\nOutput:\n%s", err, client.outputString())
	}
}

func TestPTYClientTextInputEchoesWithLoginProfileNoise(t *testing.T) {
	t.Parallel()

	home := newTestHome(t)
	noisyPrompt := strings.Repeat("p", 77) + "$ "
	profile := fmt.Sprintf("printf 'HARNESS_LOGIN_BANNER\\n'\nexport PS1=%q\n", noisyPrompt)
	if err := os.WriteFile(filepath.Join(home, ".bash_profile"), []byte(profile), 0o644); err != nil {
		t.Fatalf("writing .bash_profile: %v", err)
	}

	h := newServerHarnessForSession(t, "", home, 80, 24, "", false, false)
	client := newPTYClientHarness(t, h)

	token := fmt.Sprintf("ci-%d", time.Now().UnixNano()%1_000_000)
	client.sendText(token)

	if !client.waitForOutput(token, 5*time.Second) {
		t.Fatalf("typed text %q did not echo in client output with login profile noise\nOutput:\n%s", token, client.outputString())
	}
	if strings.Contains(client.outputString(), "HARNESS_LOGIN_BANNER") {
		t.Fatalf("pty client should ignore harness login profile output\nOutput:\n%s", client.outputString())
	}
}

func TestPTYClientLargePasteDeliversFullLine(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	client := newPTYClientHarness(t, h)

	const payloadLen = 64 * 1024
	const ready = "__READY__"
	probe := `python3 -u -c 'import sys,tty; tty.setraw(sys.stdin.fileno()); marker="".join(chr(c) for c in [95,95,82,69,65,68,89,95,95]); print(marker, flush=True); data=sys.stdin.buffer.readline(); print("__LEN=%d__" % len(data.rstrip(b"\n")), flush=True)'`

	client.sendText(probe)
	client.sendText("\r")

	if !client.waitForOutput(ready, 10*time.Second) {
		t.Fatalf("probe did not signal readiness %q\nOutput:\n%s", ready, client.outputString())
	}

	client.sendText(strings.Repeat("x", payloadLen))
	client.sendText("\n")

	want := fmt.Sprintf("__LEN=%d__", payloadLen)
	if !client.waitForOutput(want, 10*time.Second) {
		t.Fatalf("probe did not report full paste length %q\nOutput:\n%s", want, client.outputString())
	}

	client.detach()
	if !client.waitExited(5 * time.Second) {
		t.Fatalf("PTY client did not exit after detach\nOutput:\n%s", client.outputString())
	}
	if err := client.waitError(); err != nil {
		t.Fatalf("PTY client exited with error: %v\nOutput:\n%s", err, client.outputString())
	}
}

func TestPTYClientLargeUTF8PasteDeliversFullPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		chunkSize int
	}{
		{name: "4095-byte chunks", chunkSize: 4095},
		{name: "4097-byte chunks", chunkSize: 4097},
	}

	payload := largeUTF8PastePayload()
	expectedSHA := fmt.Sprintf("%x", sha256.Sum256(payload))
	expectedBytes := len(payload)
	const sentinel = "__LAB1100_UTF8_END__"

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !chunkBoundariesSplitUTF8(payload, tt.chunkSize) {
				t.Fatalf("chunk size %d never splits a UTF-8 rune boundary", tt.chunkSize)
			}

			h := newServerHarness(t)
			client := newPTYClientHarness(t, h)

			probeScriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-lab1100-paste-probe-%s-%d.py", h.session, tt.chunkSize))
			probeScript := `#!/usr/bin/env python3
import hashlib
import os
import sys
import termios
import tty

sentinel = ` + strconv.Quote(sentinel) + `.encode()
fd = sys.stdin.fileno()
orig = termios.tcgetattr(fd)
try:
    tty.setraw(fd)
    os.write(1, b"__READY__\n")
    buf = bytearray()
    while True:
        ch = os.read(fd, 1)
        if not ch:
            break
        buf += ch
        if buf.endswith(sentinel):
            del buf[-len(sentinel):]
            break
    digest = hashlib.sha256(bytes(buf)).hexdigest().encode()
    os.write(1, b"__BYTES=%d__\n" % len(buf))
    os.write(1, b"__SHA=" + digest + b"__\n")
finally:
    termios.tcsetattr(fd, termios.TCSADRAIN, orig)
`
			if err := os.WriteFile(probeScriptPath, []byte(probeScript), 0o755); err != nil {
				t.Fatalf("write probe script: %v", err)
			}
			t.Cleanup(func() { os.Remove(probeScriptPath) })

			client.sendText(probeScriptPath)
			client.sendText("\r")

			if !client.waitForOutput("__READY__", 10*time.Second) {
				t.Fatalf("probe did not signal readiness\nOutput:\n%s", client.outputString())
			}

			writeUTF8Chunks(t, client, payload, tt.chunkSize)
			client.write([]byte(sentinel))

			wantBytes := fmt.Sprintf("__BYTES=%d__", expectedBytes)
			h.waitFor("pane-1", wantBytes)
			paneOut := h.runCmd("capture", "pane-1")
			flatPaneOut := strings.ReplaceAll(paneOut, "\n", "")
			if !strings.Contains(flatPaneOut, wantBytes) {
				t.Fatalf("probe did not report full paste length %q\nPane:\n%s", wantBytes, paneOut)
			}

			wantSHA := "__SHA=" + expectedSHA + "__"
			if !strings.Contains(flatPaneOut, wantSHA) {
				t.Fatalf("probe did not report full paste SHA %q\nPane:\n%s", wantSHA, paneOut)
			}
		})
	}
}

func largeUTF8PastePayload() []byte {
	var b strings.Builder
	for i := 0; b.Len() < 18_118; i++ {
		fmt.Fprintf(&b, "ROW%03d → 72°F — 22°C — north↔south — alphaβ gammaδ — emoji🙂漢字 END%03d\n", i, i)
	}
	return []byte(b.String())
}

func chunkBoundariesSplitUTF8(payload []byte, chunkSize int) bool {
	for i := chunkSize; i < len(payload); i += chunkSize {
		if i > 0 && i < len(payload) && !isUTF8Boundary(payload, i) {
			return true
		}
	}
	return false
}

func isUTF8Boundary(payload []byte, idx int) bool {
	if idx <= 0 || idx >= len(payload) {
		return true
	}
	return (payload[idx] & 0xc0) != 0x80
}

func writeUTF8Chunks(t *testing.T, client *ptyClientHarness, payload []byte, chunkSize int) {
	t.Helper()

	for start := 0; start < len(payload); start += chunkSize {
		end := start + chunkSize
		if end > len(payload) {
			end = len(payload)
		}
		client.write(payload[start:end])
		time.Sleep(2 * time.Millisecond)
	}
}
