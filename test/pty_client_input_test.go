package test

import (
	"fmt"
	"testing"
	"time"
)

func TestPTYClientTextInputEchoesInPane(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	client := newPTYClientHarness(t, h)

	token := fmt.Sprintf("cover-input-%d", time.Now().UnixNano())
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
