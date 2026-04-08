package test

import (
	"fmt"
	"os"
	"path/filepath"
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
