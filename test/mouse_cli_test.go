package test

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMouseCommandClickStatusLineFocusesPane(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	h.splitV()
	h.assertActive("pane-2")

	h.runCmd("mouse", "click", "pane-1", "--status-line")

	if !h.waitForActive("pane-1", 3*time.Second) {
		t.Fatalf("after clicking pane-1 status line, pane-1 should be active.\nScreen:\n%s", h.capture())
	}
}

func TestMouseCommandRawDragResizesBorder(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	h.splitV()

	borderCol := h.captureAmuxVerticalBorderCol()
	if borderCol < 0 {
		t.Fatalf("no vertical border found.\nScreen:\n%s", h.captureAmux())
	}

	gen := h.generation()
	h.runCmd("mouse", "press", strconv.Itoa(borderCol+1), "10")
	h.runCmd("mouse", "motion", strconv.Itoa(borderCol+6), "10")
	h.runCmd("mouse", "release", strconv.Itoa(borderCol+6), "10")
	h.waitLayout(gen)

	newBorderCol := h.captureAmuxVerticalBorderCol()
	if newBorderCol <= borderCol {
		t.Fatalf("border should have moved right: was at %d, now at %d.\nScreen:\n%s", borderCol, newBorderCol, h.captureAmux())
	}
}

func TestMouseCommandDragPaneToPaneSwapsPanes(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	h.splitV()

	before := h.captureJSON()
	p1Before := h.jsonPane(before, "pane-1")
	p2Before := h.jsonPane(before, "pane-2")

	gen := h.generation()
	h.runCmd("mouse", "drag", "pane-2", "--to", "pane-1")
	h.waitLayout(gen)

	after := h.captureJSON()
	p1After := h.jsonPane(after, "pane-1")
	p2After := h.jsonPane(after, "pane-2")

	if p1After.Position.X != p2Before.Position.X || p1After.Position.Y != p2Before.Position.Y {
		t.Fatalf("pane-1 position = (%d,%d), want previous pane-2 position (%d,%d)", p1After.Position.X, p1After.Position.Y, p2Before.Position.X, p2Before.Position.Y)
	}
	if p2After.Position.X != p1Before.Position.X || p2After.Position.Y != p1Before.Position.Y {
		t.Fatalf("pane-2 position = (%d,%d), want previous pane-1 position (%d,%d)", p2After.Position.X, p2After.Position.Y, p1Before.Position.X, p1Before.Position.Y)
	}
}

func TestMouseCommandDragAutomaticallyCopiesSelection(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t, "SSH_CONNECTION=1")

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
	h.runCmd("mouse", "press", strconv.Itoa(startX), strconv.Itoa(y))
	h.runCmd("mouse", "motion", strconv.Itoa(endX), strconv.Itoa(y))

	h.waitUI("copy-mode-shown", 3*time.Second)

	h.runCmd("mouse", "release", strconv.Itoa(endX), strconv.Itoa(y))

	h.waitUI("copy-mode-hidden", 3*time.Second)

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
		t.Fatalf("clipboard via mouse command = %q, want %q", got, want)
	}
}
