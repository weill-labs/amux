package test

import (
	"fmt"
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
