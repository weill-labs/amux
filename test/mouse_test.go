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

	"github.com/mattn/go-runewidth"
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
		return runewidth.StringWidth(line[:col]) + 1, row + 1, true
	}
	return 0, 0, false
}

func windowTabCoords(t *testing.T, screen, label string) (x, y int) {
	t.Helper()

	x, y, ok := outerTextCoords(screen, label)
	if !ok {
		t.Fatalf("expected window tab %q in screen.\nScreen:\n%s", label, screen)
	}
	return x + runewidth.StringWidth(label)/2, y
}

func paneStatusCoords(t *testing.T, h *AmuxHarness, name string) (x, y int) {
	t.Helper()

	label := "[" + name + "]"
	screen := h.captureOuter()
	x, y, ok := outerTextCoords(screen, label)
	if !ok {
		t.Fatalf("expected pane status label %q in outer capture.\nScreen:\n%s", label, screen)
	}
	return x + runewidth.StringWidth(label)/2, y
}

func TestOuterTextCoordsUsesDisplayColumns(t *testing.T) {
	t.Parallel()

	screen := " amux │ ? help │ 1:main 2:bugs 3:docs │ 1 panes │ 12:46"
	x, y, ok := outerTextCoords(screen, "1:main")
	if !ok {
		t.Fatalf("expected to find 1:main in %q", screen)
	}
	if x != 18 || y != 1 {
		t.Fatalf("outerTextCoords(...) = (%d, %d), want (18, 1)", x, y)
	}
}

func waitForCaptureJSON(t *testing.T, h *AmuxHarness, timeout time.Duration, fn func(proto.CaptureJSON) bool) proto.CaptureJSON {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		capture := h.captureJSON()
		if fn(capture) {
			return capture
		}
		time.Sleep(50 * time.Millisecond)
	}

	capture := h.captureJSON()
	t.Fatalf("capture JSON condition not met within %v.\nCapture:\n%s\nOuter:\n%s", timeout, h.runCmd("capture", "--format", "json"), h.captureOuter())
	return capture
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

func TestMouseStatusLineDragSwapsPanes(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	h.splitV()

	startX, startY := paneStatusCoords(t, h, "pane-1")
	endX, endY := paneStatusCoords(t, h, "pane-2")

	h.sendMouseSGR(0, startX, startY, true)
	time.Sleep(50 * time.Millisecond)
	h.sendMouseSGR(32, endX, endY, true)
	time.Sleep(50 * time.Millisecond)
	h.sendMouseSGR(0, endX, endY, false)

	capture := waitForCaptureJSON(t, h, 3*time.Second, func(capture proto.CaptureJSON) bool {
		p1 := h.jsonPane(capture, "pane-1")
		p2 := h.jsonPane(capture, "pane-2")
		return p1.Position.X > p2.Position.X
	})

	p1 := h.jsonPane(capture, "pane-1")
	p2 := h.jsonPane(capture, "pane-2")
	if p1.Position.X <= p2.Position.X {
		t.Fatalf("pane-1 should swap to the right of pane-2: p1=%+v p2=%+v", p1.Position, p2.Position)
	}
}

func TestMouseStatusLineDragShowsSwapOverlay(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	h.splitV()

	startX, startY := paneStatusCoords(t, h, "pane-1")
	endX, endY := paneStatusCoords(t, h, "pane-2")

	h.sendMouseSGR(0, startX, startY, true)
	time.Sleep(50 * time.Millisecond)
	h.sendMouseSGR(32, endX, endY, true)

	if !waitForOuterContains(h, func(screen string) bool {
		return strings.Contains(screen, "[drag]") && strings.Contains(screen, "[swap]")
	}, 3*time.Second) {
		t.Fatalf("expected drag overlay while hovering swap target.\nOuter:\n%s", h.captureOuter())
	}

	time.Sleep(50 * time.Millisecond)
	h.sendMouseSGR(0, endX, endY, false)

	if !waitForOuterContains(h, func(screen string) bool {
		return !strings.Contains(screen, "[drag]") && !strings.Contains(screen, "[swap]")
	}, 3*time.Second) {
		t.Fatalf("expected drag overlay to clear after release.\nOuter:\n%s", h.captureOuter())
	}
}

func TestMouseStatusLineDragMovesPaneToOtherColumn(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	h.splitV()
	focusGen := h.generation()
	h.runCmd("focus", "pane-1")
	h.waitLayout(focusGen)
	if !h.waitForActive("pane-1", 3*time.Second) {
		t.Fatalf("pane-1 should be active before splitting left column.\nScreen:\n%s", h.capture())
	}
	h.splitH()

	borderCol := h.captureAmuxVerticalBorderCol()
	if borderCol < 0 {
		t.Fatalf("expected a root vertical border.\nScreen:\n%s", h.captureAmux())
	}

	startX, startY := paneStatusCoords(t, h, "pane-1")
	endY := h.jsonPane(h.captureJSON(), "pane-2").Position.Y + 3

	h.sendMouseSGR(0, startX, startY, true)
	time.Sleep(50 * time.Millisecond)
	h.sendMouseSGR(32, borderCol+1, endY, true)
	time.Sleep(50 * time.Millisecond)
	h.sendMouseSGR(0, borderCol+1, endY, false)

	capture := waitForCaptureJSON(t, h, 3*time.Second, func(capture proto.CaptureJSON) bool {
		p1 := h.jsonPane(capture, "pane-1")
		p2 := h.jsonPane(capture, "pane-2")
		p3 := h.jsonPane(capture, "pane-3")
		return p1.Position.X == p2.Position.X && p1.Position.Y > p2.Position.Y && p3.Position.X < p1.Position.X
	})

	p1 := h.jsonPane(capture, "pane-1")
	p2 := h.jsonPane(capture, "pane-2")
	p3 := h.jsonPane(capture, "pane-3")
	if p1.Position.X != p2.Position.X || p1.Position.Y <= p2.Position.Y || p3.Position.X >= p1.Position.X {
		t.Fatalf("expected pane-1 to move into pane-2's column below it: p1=%+v p2=%+v p3=%+v", p1.Position, p2.Position, p3.Position)
	}
}

func TestMouseStatusLineDragInsertsPaneBetweenPanesAcrossColumns(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	h.splitV()
	focusGen := h.generation()
	h.runCmd("focus", "pane-1")
	h.waitLayout(focusGen)
	if !h.waitForActive("pane-1", 3*time.Second) {
		t.Fatalf("pane-1 should be active before splitting left column.\nScreen:\n%s", h.capture())
	}
	h.splitH()

	borderRow := h.captureAmuxHorizontalBorderRow()
	if borderRow < 0 {
		t.Fatalf("expected a horizontal border inside the left column.\nScreen:\n%s", h.captureAmux())
	}

	startX, startY := paneStatusCoords(t, h, "pane-2")
	endX, _ := paneStatusCoords(t, h, "pane-1")

	h.sendMouseSGR(0, startX, startY, true)
	time.Sleep(50 * time.Millisecond)
	h.sendMouseSGR(32, endX, borderRow+1, true)
	time.Sleep(50 * time.Millisecond)
	h.sendMouseSGR(0, endX, borderRow+1, false)

	capture := waitForCaptureJSON(t, h, 3*time.Second, func(capture proto.CaptureJSON) bool {
		p1 := h.jsonPane(capture, "pane-1")
		p2 := h.jsonPane(capture, "pane-2")
		p3 := h.jsonPane(capture, "pane-3")
		return p1.Position.X == p2.Position.X &&
			p2.Position.X == p3.Position.X &&
			p1.Position.Y < p2.Position.Y &&
			p2.Position.Y < p3.Position.Y
	})

	p1 := h.jsonPane(capture, "pane-1")
	p2 := h.jsonPane(capture, "pane-2")
	p3 := h.jsonPane(capture, "pane-3")
	if p1.Position.X != p2.Position.X || p2.Position.X != p3.Position.X || p1.Position.Y >= p2.Position.Y || p2.Position.Y >= p3.Position.Y {
		t.Fatalf("expected pane-2 to move between pane-1 and pane-3 in the left column: p1=%+v p2=%+v p3=%+v", p1.Position, p2.Position, p3.Position)
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

func TestMouseClickPassThroughAppMouse(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	scriptPath := writeMouseScript(t, h, "mouse-click-pass", `#!/bin/bash
orig=$(stty -g)
trap 'stty "$orig"' EXIT
stty raw -echo
python3 -c "$(cat <<'PY'
import os

os.write(1, b"\x1b[?1000h\x1b[?1006hREADY\n")
events = []
for _ in range(2):
    buf = b''
    while not buf.endswith(b"M") and not buf.endswith(b"m"):
        chunk = os.read(0, 1)
        if not chunk:
            break
        buf += chunk
    events.append(buf.hex())

print("CLICK1=" + events[0], flush=True)
print("CLICK2=" + events[1], flush=True)
PY
)"
`)
	h.sendKeys(scriptPath, "Enter")
	if !h.waitFor("READY", 3*time.Second) {
		t.Fatalf("expected click pass-through script to arm mouse mode.\nScreen:\n%s", h.captureOuter())
	}

	h.clickAt(40, 12)

	if !h.waitFor("CLICK1=", 3*time.Second) || !h.waitFor("CLICK2=", 3*time.Second) {
		t.Fatalf("expected active pane to receive click events.\nScreen:\n%s", h.captureOuter())
	}
	screen := h.captureOuter()
	if !strings.Contains(screen, "1b5b3c303b34303b31314d") {
		t.Fatalf("expected click press sequence to reach pane input.\nScreen:\n%s", screen)
	}
	if !strings.Contains(screen, "1b5b3c303b34303b31316d") {
		t.Fatalf("expected click release sequence to reach pane input.\nScreen:\n%s", screen)
	}
	if strings.Contains(screen, "[copy]") {
		t.Fatalf("app mouse click pass-through should not enter copy mode.\nScreen:\n%s", screen)
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

	// Keep the target line visible long enough for slower CI runners to capture
	// its screen coordinates before the shell prompt redraws.
	h.sendKeys("printf '\\033[2J\\033[Hhello from mouse\\n'; sleep 1", "Enter")
	if !h.waitFor("hello from mouse", 3*time.Second) {
		t.Fatalf("expected mouse copy target output.\nScreen:\n%s", h.captureOuter())
	}

	genStr := strings.TrimSpace(h.outer.runCmd("cursor", "clipboard"))
	gen, err := strconv.ParseUint(genStr, 10, 64)
	if err != nil {
		t.Fatalf("parsing outer clipboard generation %q: %v", genStr, err)
	}

	var (
		screen string
		startX int
		y      int
		ok     bool
	)
	if !h.waitForOuterFunc(func(cur string) bool {
		startX, y, ok = outerTextCoords(cur, "hello from mouse")
		if ok {
			screen = cur
		}
		return ok
	}, 3*time.Second) {
		screen = h.captureOuter()
		startX, y, ok = outerTextCoords(screen, "hello from mouse")
	}
	if !ok {
		t.Fatalf("expected visible mouse copy target in outer capture.\nScreen:\n%s", screen)
	}
	endX := startX + len("hello") - 1
	h.sendMouseSGR(0, startX, y, true)
	h.sendMouseSGR(32, endX, y, true)

	h.waitUI(proto.UIEventCopyModeShown, 3*time.Second)

	h.sendMouseSGR(0, endX, y, false)

	h.waitUI(proto.UIEventCopyModeHidden, 3*time.Second)

	out := strings.TrimSpace(h.outer.runCmd("wait", "clipboard", "--after", strconv.FormatUint(gen, 10), "--timeout", "5s"))
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
