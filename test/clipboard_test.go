package test

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
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

func TestClipboardOSC52(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Read clipboard generation before emitting OSC 52
	genStr := strings.TrimSpace(h.runCmd("clipboard-gen"))
	gen, _ := strconv.ParseUint(genStr, 10, 64)

	// Emit OSC 52 with "Hello" (base64: SGVsbG8=), BEL terminator
	h.sendKeys("printf '\\033]52;c;SGVsbG8=\\007'", "Enter")

	// Block until the inner server processes the OSC 52 event
	out := strings.TrimSpace(h.runCmd("wait-clipboard", "--after", strconv.FormatUint(gen, 10), "--timeout", "5s"))
	if strings.Contains(out, "timeout") {
		t.Fatalf("wait-clipboard timed out")
	}

	b64 := extractOSC52Base64(out)
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decoding clipboard base64 %q (from %q): %v", b64, out, err)
	}

	if string(decoded) != "Hello" {
		t.Errorf("clipboard via OSC 52: got %q, want %q", string(decoded), "Hello")
	}
}

func TestClipboardOSC52STTerminator(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	genStr := strings.TrimSpace(h.runCmd("clipboard-gen"))
	gen, _ := strconv.ParseUint(genStr, 10, 64)

	// Emit OSC 52 with ST terminator (\033\\) instead of BEL
	// "World" = V29ybGQ= in base64
	h.sendKeys("printf '\\033]52;c;V29ybGQ=\\033\\\\'", "Enter")

	out := strings.TrimSpace(h.runCmd("wait-clipboard", "--after", strconv.FormatUint(gen, 10), "--timeout", "5s"))
	if strings.Contains(out, "timeout") {
		t.Fatalf("wait-clipboard timed out")
	}

	b64 := extractOSC52Base64(out)
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decoding clipboard base64 %q (from %q): %v", b64, out, err)
	}

	if string(decoded) != "World" {
		t.Errorf("clipboard via OSC 52 (ST terminator): got %q, want %q", string(decoded), "World")
	}
}
