package test

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func TestMouseClickFocus(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitV()

	h.assertActive("pane-2")

	// Click at column 10, row 5 — inside pane-1 (left half of 80-col terminal)
	h.clickAt(10, 5)

	if !h.waitForActive("pane-1", 3*time.Second) {
		t.Errorf("after clicking left pane, pane-1 should be active.\nScreen:\n%s", h.capture())
	}

	// Click on pane-2 (column 60) to switch back
	h.clickAt(60, 5)

	if !h.waitForActive("pane-2", 3*time.Second) {
		t.Errorf("after clicking right pane, pane-2 should be active.\nScreen:\n%s", h.capture())
	}
}

func TestMouseClickFocusHorizontalSplit(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitH()

	h.assertActive("pane-2")

	// Click at top of screen (row 3) — inside pane-1
	h.clickAt(40, 3)

	if !h.waitForActive("pane-1", 3*time.Second) {
		t.Errorf("after clicking top pane, pane-1 should be active.\nScreen:\n%s", h.capture())
	}
}

func TestMouseClickInsideZoomedPaneDoesNotUnzoom(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitH()
	gen := h.generation()
	h.runCmd("zoom", "pane-2")
	h.waitLayout(gen)

	h.assertScreen("pane-2 should be zoomed before click", func(s string) bool {
		return strings.Contains(s, "[pane-2]") && !strings.Contains(s, "[pane-1]")
	})

	gen = h.generation()
	h.clickAt(40, 3)

	if h.waitLayoutOrTimeout(gen, "500ms") {
		t.Fatalf("clicking inside zoomed pane should not change layout.\nScreen:\n%s", h.capture())
	}

	h.assertScreen("clicking inside zoomed pane should stay zoomed", func(s string) bool {
		return strings.Contains(s, "[pane-2]") && !strings.Contains(s, "[pane-1]")
	})
}

func TestMouseBorderDrag(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitV()

	borderCol := h.captureAmuxVerticalBorderCol()
	if borderCol < 0 {
		t.Fatalf("no vertical border found.\nScreen:\n%s", h.captureAmux())
	}

	dragDelta := 5
	gen := h.generation()
	h.dragBorder(borderCol+1, 10, borderCol+1+dragDelta, 10)
	h.waitLayout(gen)

	newBorderCol := h.captureAmuxVerticalBorderCol()
	if newBorderCol < 0 {
		t.Fatalf("no vertical border found after drag.\nScreen:\n%s", h.captureAmux())
	}
	if newBorderCol <= borderCol {
		t.Errorf("border should have moved right: was at %d, now at %d.\nScreen:\n%s",
			borderCol, newBorderCol, h.captureAmux())
	}
}

func writeMouseScript(t *testing.T, h *AmuxHarness, name, body string) string {
	t.Helper()
	path := filepath.Join(os.TempDir(), fmt.Sprintf("%s-%s-%s.sh", name, h.session, t.Name()))
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	t.Cleanup(func() { os.Remove(path) })
	return path
}

func waitForOuterContains(h *AmuxHarness, fn func(string) bool, timeout time.Duration) bool {
	h.tb.Helper()
	return h.waitForOuterFunc(fn, timeout)
}

func firstMarkerNumber(screen, prefix string) int {
	for _, line := range strings.Split(screen, "\n") {
		idx := strings.Index(line, prefix)
		if idx < 0 {
			continue
		}
		start := idx + len(prefix)
		end := start
		for end < len(line) && line[end] >= '0' && line[end] <= '9' {
			end++
		}
		if end == start {
			continue
		}
		n, err := strconv.Atoi(line[start:end])
		if err == nil {
			return n
		}
	}
	return 0
}

func outerTextCoords(screen, substr string) (x, y int, ok bool) {
	for row, line := range strings.Split(screen, "\n") {
		col := strings.Index(line, substr)
		if col < 0 {
			continue
		}
		return col + 1, row + 1, true
	}
	return 0, 0, false
}

func windowTabCoords(t *testing.T, screen, label string) (x, y int) {
	t.Helper()

	x, y, ok := outerTextCoords(screen, label)
	if !ok {
		t.Fatalf("expected window tab %q in screen.\nScreen:\n%s", label, screen)
	}
	return x + len(label)/2, y
}

func TestMouseHorizontalBorderDrag(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitH()

	borderRow := h.captureAmuxHorizontalBorderRow()
	if borderRow < 0 {
		t.Fatalf("no horizontal border found.\nScreen:\n%s", h.captureAmux())
	}

	dragDelta := 3
	gen := h.generation()
	h.dragBorder(40, borderRow+1, 40, borderRow+1+dragDelta)
	h.waitLayout(gen)

	newBorderRow := h.captureAmuxHorizontalBorderRow()
	if newBorderRow < 0 {
		t.Fatalf("no horizontal border found after drag.\nScreen:\n%s", h.captureAmux())
	}
	if newBorderRow <= borderRow {
		t.Errorf("border should have moved down: was at %d, now at %d.\nScreen:\n%s",
			borderRow, newBorderRow, h.captureAmux())
	}
}

func TestMouseClickWindowTabsSwitchWindows(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	h.runCmd("rename-window", "main")

	h.sendKeys("C-a", "c")
	if !h.waitFor("[pane-2]", 3*time.Second) {
		t.Fatalf("window 2 did not appear.\nScreen:\n%s", h.capture())
	}
	h.runCmd("rename-window", "bugs")

	h.sendKeys("C-a", "c")
	if !h.waitFor("[pane-3]", 3*time.Second) {
		t.Fatalf("window 3 did not appear.\nScreen:\n%s", h.capture())
	}
	h.runCmd("rename-window", "docs")

	tests := []struct {
		label    string
		wantPane string
	}{
		{label: "1:main", wantPane: "[pane-1]"},
		{label: "2:bugs", wantPane: "[pane-2]"},
		{label: "3:docs", wantPane: "[pane-3]"},
	}

	for _, tt := range tests {
		screen := h.captureOuter()
		x, y := windowTabCoords(t, screen, tt.label)
		h.clickAt(x, y)
		if !h.waitFor(tt.wantPane, 3*time.Second) {
			t.Fatalf("clicking %s should show %s.\nScreen:\n%s", tt.label, tt.wantPane, h.capture())
		}
	}
}

func TestMouseClickGlobalBarOutsideTabsDoesNothing(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	h.sendKeys("C-a", "c")
	if !h.waitFor("[pane-2]", 3*time.Second) {
		t.Fatalf("window 2 did not appear.\nScreen:\n%s", h.capture())
	}

	screen := h.captureOuter()
	x, y, ok := outerTextCoords(screen, "panes")
	if !ok {
		t.Fatalf("expected panes count in global bar.\nScreen:\n%s", screen)
	}

	gen := h.generation()
	h.clickAt(x, y)
	if h.waitLayoutOrTimeout(gen, "500ms") {
		t.Fatalf("clicking outside window tabs should not change layout.\nScreen:\n%s", h.capture())
	}
	if got := h.activePaneName(); got != "pane-2" {
		t.Fatalf("active pane after outside click = %s, want pane-2", got)
	}
}

func TestMouseClickGlobalBarSingleWindowDoesNothing(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	screen := h.captureOuter()
	x, y, ok := outerTextCoords(screen, "amux")
	if !ok {
		t.Fatalf("expected amux prefix in global bar.\nScreen:\n%s", screen)
	}

	gen := h.generation()
	h.clickAt(x, y)
	if h.waitLayoutOrTimeout(gen, "500ms") {
		t.Fatalf("clicking the single-window global bar should not change layout.\nScreen:\n%s", h.capture())
	}
	if got := h.activePaneName(); got != "pane-1" {
		t.Fatalf("active pane after single-window click = %s, want pane-1", got)
	}
}

func TestMouseScrollWheel(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	scriptPath := writeMouseScript(t, h, "mouse-scroll", `#!/bin/bash
for i in $(seq -w 1 24); do echo "MWHEEL-$i"; done
`)
	h.sendKeys(scriptPath, "Enter")
	h.waitFor("MWHEEL-24", 3*time.Second)

	screen := h.captureOuter()
	beforeTop := firstMarkerNumber(screen, "MWHEEL-")
	if beforeTop == 0 {
		t.Fatalf("expected visible MWHEEL output before wheel-up.\nScreen:\n%s", screen)
	}
	if strings.Contains(screen, "MWHEEL-02") {
		t.Fatalf("expected earlier scrollback to be off-screen before wheel-up.\nScreen:\n%s", screen)
	}

	h.scrollAt(40, 12, true)

	h.waitUI(proto.UIEventCopyModeShown, 3*time.Second)
	if !waitForOuterContains(h, func(s string) bool {
		afterTop := firstMarkerNumber(s, "MWHEEL-")
		return afterTop > 0 && afterTop < beforeTop
	}, 3*time.Second) {
		t.Fatalf("expected wheel-up to reveal earlier scrollback.\nScreen:\n%s", h.captureOuter())
	}
}

func TestMouseScrollWheelDownExitsCopyMode(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	scriptPath := writeMouseScript(t, h, "mouse-exit", `#!/bin/bash
for i in $(seq -w 1 24); do echo "MEXIT-$i"; done
`)
	h.sendKeys(scriptPath, "Enter")
	h.waitFor("MEXIT-24", 3*time.Second)

	h.scrollAt(40, 12, true)
	h.waitUI(proto.UIEventCopyModeShown, 3*time.Second)

	h.scrollAt(40, 12, false)
	h.waitUI(proto.UIEventCopyModeHidden, 3*time.Second)
}

func TestMouseScrollWheelTargetsInactivePaneWithoutFocusChange(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)
	h.splitV()
	h.assertActive("pane-2")

	scriptPath := writeMouseScript(t, h, "mouse-inactive", `#!/bin/bash
for i in $(seq -w 1 24); do echo "MINACTIVE-$i"; done
`)
	h.runCmd("send-keys", "pane-1", scriptPath, "Enter")
	if !h.waitFor("MINACTIVE-24", 3*time.Second) {
		t.Fatalf("expected left pane output before wheel-up.\nScreen:\n%s", h.captureOuter())
	}

	h.scrollAt(10, 5, true)
	h.waitUI(proto.UIEventCopyModeShown, 3*time.Second)
	if got := h.activePaneName(); got != "pane-2" {
		t.Fatalf("wheel-up entry should not immediately change focus: active=%s", got)
	}
}

func TestMouseScrollWheelPassThroughAppMouse(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	scriptPath := writeMouseScript(t, h, "mouse-pass", `#!/bin/bash
orig=$(stty -g)
trap 'stty "$orig"' EXIT
stty raw -echo
python3 -c "$(cat <<'PY'
import os

os.write(1, b"\x1b[?1002h\x1b[?1006hREADY\n")
events = []
for _ in range(2):
    buf = b''
    while not buf.endswith(b"M") and not buf.endswith(b"m"):
        chunk = os.read(0, 1)
        if not chunk:
            break
        buf += chunk
    events.append(buf.hex())

print("MOUSE1=" + events[0], flush=True)
print("MOUSE2=" + events[1], flush=True)
PY
)"
`)
	h.sendKeys(scriptPath, "Enter")
	if !h.waitFor("READY", 3*time.Second) {
		t.Fatalf("expected pass-through script to arm mouse mode.\nScreen:\n%s", h.captureOuter())
	}

	h.scrollAt(40, 12, true)
	h.scrollAt(40, 12, false)

	if !h.waitFor("MOUSE1=", 3*time.Second) || !h.waitFor("MOUSE2=", 3*time.Second) {
		t.Fatalf("expected active pane to receive wheel events.\nScreen:\n%s", h.captureOuter())
	}
	screen := h.captureOuter()
	if !strings.Contains(screen, "1b5b3c36343b34303b31314d") {
		t.Fatalf("expected wheel-up sequence to reach pane input.\nScreen:\n%s", screen)
	}
	if !strings.Contains(screen, "1b5b3c36353b34303b31314d") {
		t.Fatalf("expected wheel-down sequence to reach pane input.\nScreen:\n%s", screen)
	}
	if strings.Contains(screen, "[copy]") {
		t.Fatalf("app mouse pass-through should not enter copy mode.\nScreen:\n%s", screen)
	}
}

func TestMouseDragCopiesSelectionInCopyMode(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	scriptPath := writeMouseScript(t, h, "mouse-copy", `#!/bin/bash
echo "MOUSE COPY TARGET"
`)
	h.sendKeys(scriptPath, "Enter")
	if !h.waitFor("MOUSE COPY TARGET", 3*time.Second) {
		t.Fatalf("expected copy target output.\nScreen:\n%s", h.captureOuter())
	}

	h.sendKeys("C-a", "[")
	h.waitUI(proto.UIEventCopyModeShown, 3*time.Second)

	y := 2
	h.sendMouseSGR(0, 1, y, true)
	h.sendMouseSGR(32, 5, y, true)
	h.sendMouseSGR(0, 5, y, false)

	h.waitUI(proto.UIEventCopyModeHidden, 3*time.Second)
}

func TestMouseDragAutomaticallyEntersCopyModeAndCopiesSelection(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t, "SSH_CONNECTION=1")

	h.sendKeys("printf '\\033[2J\\033[Hhello from mouse\\n'; sleep 0.2", "Enter")
	if !h.waitFor("hello from mouse", 3*time.Second) {
		t.Fatalf("expected mouse copy target output.\nScreen:\n%s", h.captureOuter())
	}

	genStr := strings.TrimSpace(h.outer.runCmd("clipboard-gen"))
	gen, err := strconv.ParseUint(genStr, 10, 64)
	if err != nil {
		t.Fatalf("parsing outer clipboard generation %q: %v", genStr, err)
	}

	screen := h.captureOuter()
	startX, y, ok := outerTextCoords(screen, "hello from mouse")
	if !ok {
		t.Fatalf("expected visible mouse copy target in outer capture.\nScreen:\n%s", screen)
	}
	endX := startX + len("hello") - 1
	h.sendMouseSGR(0, startX, y, true)
	h.sendMouseSGR(32, endX, y, true)

	h.waitUI(proto.UIEventCopyModeShown, 3*time.Second)

	h.sendMouseSGR(0, endX, y, false)

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
	if got, want := string(decoded), "hello"; got != want {
		t.Fatalf("clipboard via auto mouse copy = %q, want %q", got, want)
	}
}

func TestMouseDoubleClickCopiesWordInCopyMode(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	scriptPath := writeMouseScript(t, h, "mouse-double", `#!/bin/bash
echo "DOUBLE CLICK TARGET"
`)
	h.sendKeys(scriptPath, "Enter")
	if !h.waitFor("DOUBLE CLICK TARGET", 3*time.Second) {
		t.Fatalf("expected double-click target output.\nScreen:\n%s", h.captureOuter())
	}

	h.sendKeys("C-a", "[")
	h.waitUI(proto.UIEventCopyModeShown, 3*time.Second)

	y := 2
	h.clickAt(2, y)
	h.clickAt(2, y)

	h.waitUI(proto.UIEventCopyModeHidden, 3*time.Second)
}

func TestMouseTripleClickCopiesLineInCopyMode(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	scriptPath := writeMouseScript(t, h, "mouse-triple", `#!/bin/bash
echo "TRIPLE CLICK TARGET"
`)
	h.sendKeys(scriptPath, "Enter")
	if !h.waitFor("TRIPLE CLICK TARGET", 3*time.Second) {
		t.Fatalf("expected triple-click target output.\nScreen:\n%s", h.captureOuter())
	}

	h.sendKeys("C-a", "[")
	h.waitUI(proto.UIEventCopyModeShown, 3*time.Second)

	y := 2
	h.clickAt(2, y)
	h.clickAt(2, y)
	h.clickAt(2, y)

	h.waitUI(proto.UIEventCopyModeHidden, 3*time.Second)
}

func TestMouseScrollWheelIgnoredInAltScreenWithoutMouseMode(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	scriptPath := writeMouseScript(t, h, "mouse-alt", `#!/bin/bash
printf '\033[?1049hALTSCREEN'
sleep 2
`)
	h.sendKeys(scriptPath, "Enter")
	if !h.waitFor("ALTSCREEN", 3*time.Second) {
		t.Fatalf("expected alt-screen test program output.\nScreen:\n%s", h.captureOuter())
	}

	h.scrollAt(40, 12, true)
	if waitForOuterContains(h, func(s string) bool {
		return strings.Contains(s, "[copy]")
	}, 750*time.Millisecond) {
		t.Fatalf("wheel-up in alt-screen without mouse mode should not enter copy mode.\nScreen:\n%s", h.captureOuter())
	}
}
