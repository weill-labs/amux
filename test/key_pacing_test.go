package test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTimingSensitiveScript(t *testing.T, session string) string {
	t.Helper()
	path := filepath.Join(os.TempDir(), fmt.Sprintf("amux-key-pacing-%s-%s.sh", session, t.Name()))
	script := `#!/bin/bash
orig=$(stty -g)
trap 'stty "$orig"' EXIT
stty raw -echo
python3 -c "$(cat <<'PY'
import os
import time

os.write(1, b"READY\n")
buf = bytearray()
last = None

while True:
    ch = os.read(0, 1)
    if not ch:
        break
    now = time.monotonic()
    if ch == b"\r":
        if last is not None and now - last >= 0.02 and buf:
            os.write(1, b"SUBMIT=" + bytes(buf) + b"\n")
        else:
            os.write(1, b"EARLY_ENTER\n")
        buf.clear()
        last = now
        continue
    buf.extend(ch)
    last = now
PY
)"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	t.Cleanup(func() { os.Remove(path) })
	return path
}

func TestSendKeysPacesEnterAfterText(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	scriptPath := writeTimingSensitiveScript(t, h.session)
	h.sendKeys("pane-1", scriptPath, "Enter")
	h.waitFor("pane-1", "READY")

	out := h.runCmd("send-keys", "pane-1", "HELLO", "Enter")
	if strings.Contains(out, "invalid") {
		t.Fatalf("send-keys failed: %s", out)
	}
	h.waitFor("pane-1", "SUBMIT=HELLO")

	paneOut := h.runCmd("capture", "pane-1")
	if strings.Contains(paneOut, "EARLY_ENTER") {
		t.Fatalf("send-keys should not batch Enter with preceding text\npane:\n%s", paneOut)
	}
}

func TestTypeKeysPacesEnterAfterText(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	scriptPath := writeTimingSensitiveScript(t, h.session)
	h.sendKeys(scriptPath, "Enter")
	if !h.waitFor("READY", 3*time.Second) {
		t.Fatalf("expected timing-sensitive reader to arm\nscreen:\n%s", h.captureOuter())
	}

	h.runCmd("type-keys", "HELLO", "Enter")
	if !h.waitFor("SUBMIT=HELLO", 3*time.Second) {
		t.Fatalf("expected HELLO submit after paced type-keys\nscreen:\n%s", h.captureOuter())
	}
	if strings.Contains(h.captureOuter(), "EARLY_ENTER") {
		t.Fatalf("type-keys should not batch Enter with preceding text\nscreen:\n%s", h.captureOuter())
	}
}
