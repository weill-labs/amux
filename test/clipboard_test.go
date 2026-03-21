package test

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

// extractOSC52Base64 extracts the base64 payload from a raw OSC 52 sequence.
// Format: \x1b]52;<selection>;<base64-data><terminator>
func extractOSC52Base64(raw string) string {
	// Find the second semicolon (after "52;c;")
	prefix := "\x1b]52;"
	if !strings.HasPrefix(raw, prefix) {
		return raw
	}
	rest := raw[len(prefix):]
	idx := strings.IndexByte(rest, ';')
	if idx < 0 {
		return raw
	}
	payload := rest[idx+1:]
	// Strip terminator: BEL (\x07) or ST (\x1b\)
	payload = strings.TrimRight(payload, "\x07")
	payload = strings.TrimSuffix(payload, "\x1b\\")
	return payload
}

// assertClipboardOSC52 emits an OSC 52 sequence via printf, waits for the
// clipboard event, and asserts the decoded content matches want.
func assertClipboardOSC52(t *testing.T, h *AmuxHarness, printfArg, want string) {
	t.Helper()
	genStr := strings.TrimSpace(h.runCmd("clipboard-gen"))
	gen, _ := strconv.ParseUint(genStr, 10, 64)

	h.sendKeys("printf '"+printfArg+"'", "Enter")

	out := strings.TrimSpace(h.runCmd("wait-clipboard", "--after", strconv.FormatUint(gen, 10), "--timeout", "5s"))
	if strings.Contains(out, "timeout") {
		t.Fatalf("wait-clipboard timed out")
	}

	b64 := extractOSC52Base64(out)
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decoding clipboard base64 %q (from %q): %v", b64, out, err)
	}

	if string(decoded) != want {
		t.Errorf("clipboard via OSC 52: got %q, want %q", string(decoded), want)
	}
}

func TestClipboardOSC52(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)
	// "Hello" = SGVsbG8= in base64, BEL terminator
	assertClipboardOSC52(t, h, "\\033]52;c;SGVsbG8=\\007", "Hello")
}

func TestClipboardOSC52STTerminator(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)
	// "World" = V29ybGQ= in base64, ST terminator (\033\\)
	assertClipboardOSC52(t, h, "\\033]52;c;V29ybGQ=\\033\\\\", "World")
}

func TestCopyModeClipboardUsesOSC52WhenInnerClientRunsOverSSH(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t, "SSH_CONNECTION=1")

	h.sendKeys("echo SSH-COPY-TEST", "Enter")
	if !h.waitFor("SSH-COPY-TEST", 3*time.Second) {
		t.Fatalf("expected SSH-COPY-TEST in output\nScreen:\n%s", h.captureOuter())
	}

	genStr := strings.TrimSpace(h.outer.runCmd("clipboard-gen"))
	gen, err := strconv.ParseUint(genStr, 10, 64)
	if err != nil {
		t.Fatalf("parsing outer clipboard generation %q: %v", genStr, err)
	}

	h.sendKeys("C-a", "[")
	h.waitUI(proto.UIEventCopyModeShown, 3*time.Second)

	h.sendKeys("/")
	if !h.waitFor("[copy] /", 3*time.Second) {
		t.Fatalf("expected copy-mode search prompt\nScreen:\n%s", h.captureOuter())
	}

	h.sendKeys("SSH-COPY-TEST")
	if !h.waitFor("/SSH-COPY-TEST", 3*time.Second) {
		t.Fatalf("expected search query in copy-mode status\nScreen:\n%s", h.captureOuter())
	}

	h.sendKeys("Enter")
	h.sendKeys("Enter")
	h.waitUI(proto.UIEventCopyModeHidden, 3*time.Second)

	out := strings.TrimSpace(h.outer.runCmd("wait-clipboard", "--after", strconv.FormatUint(gen, 10), "--timeout", "5s"))
	if strings.Contains(out, "timeout") {
		t.Fatalf("outer wait-clipboard timed out\nOuter:\n%s", h.captureOuter())
	}

	b64 := extractOSC52Base64(out)
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decoding clipboard base64 %q (from %q): %v", b64, out, err)
	}
	if got, want := string(decoded), "SSH-COPY-TEST"; got != want {
		t.Fatalf("clipboard via nested OSC 52 = %q, want %q", got, want)
	}
}
