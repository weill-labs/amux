package test

import (
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func TestKittyKeyboardPrefixSplit(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t, "AMUX_CLIENT_CAPABILITIES=kitty_keyboard")

	gen := h.generation()
	h.sendKeysHex([]byte("\x1b[97;5u"))
	h.sendKeys("-")
	h.waitLayout(gen)

	out := h.runCmd("status")
	if !strings.Contains(out, "2 total") {
		t.Fatalf("expected 2 panes after kitty ctrl-a prefix split, got: %s", out)
	}
}

func TestKittyKeyboardAltFocus(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t, "AMUX_CLIENT_CAPABILITIES=kitty_keyboard")
	h.splitV()
	h.assertActive("pane-2")

	h.sendKeysHex([]byte("\x1b[104;3u"))
	if !h.waitForActive("pane-1", 3*time.Second) {
		t.Fatalf("expected kitty alt-h to focus pane-1\nScreen:\n%s", h.captureOuter())
	}

	h.sendKeysHex([]byte("\x1b[108;3u"))
	if !h.waitForActive("pane-2", 3*time.Second) {
		t.Fatalf("expected kitty alt-l to focus pane-2\nScreen:\n%s", h.captureOuter())
	}
}

func TestKittyKeyboardChooserEscape(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t, "AMUX_CLIENT_CAPABILITIES=kitty_keyboard")

	h.sendKeysHex([]byte("\x1b[97;5u"))
	h.sendKeys("s")
	out := h.runCmd("wait-ui", proto.UIEventChooseTreeShown, "--timeout", "3s")
	if !strings.Contains(out, proto.UIEventChooseTreeShown) {
		t.Fatalf("expected chooser shown event, got: %s", out)
	}

	h.sendKeysHex([]byte("\x1b[27u"))
	out = h.runCmd("wait-ui", proto.UIEventChooseTreeHidden, "--timeout", "3s")
	if !strings.Contains(out, proto.UIEventChooseTreeHidden) {
		t.Fatalf("expected chooser hidden event after kitty escape, got: %s", out)
	}
}
