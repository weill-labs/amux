package test

import (
	"reflect"
	"strings"
	"testing"
)

func TestEqualizeCommandHorizontal(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.unsetLead()
	h.runCmd("split", "pane-1", "root", "v")
	h.runCmd("split", "pane-1", "root", "v")

	if out := h.runCmd("resize-pane", "pane-1", "right", "6"); !strings.Contains(out, "Resized") {
		t.Fatalf("resize-pane failed: %s", out)
	}

	out := h.runCmd("equalize")
	if !strings.Contains(out, "Equalized") {
		t.Fatalf("equalize output = %q, want Equalized confirmation", out)
	}

	capture := h.captureJSON()
	widths := []int{
		h.jsonPane(capture, "pane-1").Position.Width,
		h.jsonPane(capture, "pane-2").Position.Width,
		h.jsonPane(capture, "pane-3").Position.Width,
	}
	if !reflect.DeepEqual(widths, []int{26, 26, 26}) {
		t.Fatalf("equalized root column widths = %v, want [26 26 26]", widths)
	}
}

func TestEqualizeCommandVertical(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.unsetLead()
	h.runCmd("split", "pane-1", "root", "v")
	h.runCmd("split", "pane-1", "root", "v")
	h.runCmd("focus", "pane-2")
	h.runCmd("split", "pane-2")
	h.runCmd("split", "pane-2")

	if out := h.runCmd("resize-pane", "pane-2", "down", "3"); !strings.Contains(out, "Resized") {
		t.Fatalf("resize-pane failed: %s", out)
	}

	before := h.captureJSON()
	widthsBefore := []int{
		h.jsonPane(before, "pane-1").Position.Width,
		h.jsonPane(before, "pane-2").Position.Width,
		h.jsonPane(before, "pane-3").Position.Width,
	}

	out := h.runCmd("equalize", "--vertical")
	if !strings.Contains(out, "Equalized") {
		t.Fatalf("equalize --vertical output = %q, want Equalized confirmation", out)
	}

	after := h.captureJSON()
	widthsAfter := []int{
		h.jsonPane(after, "pane-1").Position.Width,
		h.jsonPane(after, "pane-2").Position.Width,
		h.jsonPane(after, "pane-3").Position.Width,
	}
	if !reflect.DeepEqual(widthsAfter, widthsBefore) {
		t.Fatalf("equalize --vertical changed widths = %v, want %v", widthsAfter, widthsBefore)
	}

	heights := []int{
		h.jsonPane(after, "pane-2").Position.Height,
		h.jsonPane(after, "pane-4").Position.Height,
		h.jsonPane(after, "pane-5").Position.Height,
	}
	if !reflect.DeepEqual(heights, []int{7, 7, 7}) {
		t.Fatalf("equalize --vertical heights = %v, want [7 7 7]", heights)
	}
}

func TestEqualizeCommandAll(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.unsetLead()
	h.runCmd("split", "pane-1", "root", "v")
	h.runCmd("split", "pane-1", "root", "v")
	h.runCmd("focus", "pane-2")
	h.runCmd("split", "pane-2")
	h.runCmd("split", "pane-2")

	if out := h.runCmd("resize-pane", "pane-1", "right", "6"); !strings.Contains(out, "Resized") {
		t.Fatalf("resize-pane width skew failed: %s", out)
	}
	if out := h.runCmd("resize-pane", "pane-2", "down", "3"); !strings.Contains(out, "Resized") {
		t.Fatalf("resize-pane height skew failed: %s", out)
	}

	out := h.runCmd("equalize", "--all")
	if !strings.Contains(out, "Equalized") {
		t.Fatalf("equalize --all output = %q, want Equalized confirmation", out)
	}

	capture := h.captureJSON()
	widths := []int{
		h.jsonPane(capture, "pane-1").Position.Width,
		h.jsonPane(capture, "pane-2").Position.Width,
		h.jsonPane(capture, "pane-3").Position.Width,
	}
	if !reflect.DeepEqual(widths, []int{26, 26, 26}) {
		t.Fatalf("equalize --all widths = %v, want [26 26 26]", widths)
	}

	heights := []int{
		h.jsonPane(capture, "pane-2").Position.Height,
		h.jsonPane(capture, "pane-4").Position.Height,
		h.jsonPane(capture, "pane-5").Position.Height,
	}
	if !reflect.DeepEqual(heights, []int{7, 7, 7}) {
		t.Fatalf("equalize --all heights = %v, want [7 7 7]", heights)
	}
}

func TestEqualizeKeybindingHorizontal(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	h.unsetLead()
	h.runCmd("split", "pane-1", "root", "v")
	h.runCmd("split", "pane-1", "root", "v")
	h.runCmd("focus", "pane-2")
	h.runCmd("split", "pane-2")
	h.runCmd("split", "pane-2")

	if out := h.runCmd("resize-pane", "pane-1", "right", "6"); !strings.Contains(out, "Resized") {
		t.Fatalf("resize-pane width skew failed: %s", out)
	}
	if out := h.runCmd("resize-pane", "pane-2", "down", "3"); !strings.Contains(out, "Resized") {
		t.Fatalf("resize-pane height skew failed: %s", out)
	}

	before := h.captureJSON()
	heightsBefore := []int{
		h.jsonPane(before, "pane-2").Position.Height,
		h.jsonPane(before, "pane-4").Position.Height,
		h.jsonPane(before, "pane-5").Position.Height,
	}

	gen := h.generation()
	h.sendKeys("C-a", "=")
	h.waitLayout(gen)

	after := h.captureJSON()
	widths := []int{
		h.jsonPane(after, "pane-1").Position.Width,
		h.jsonPane(after, "pane-2").Position.Width,
		h.jsonPane(after, "pane-3").Position.Width,
	}
	if !reflect.DeepEqual(widths, []int{26, 26, 26}) {
		t.Fatalf("Ctrl-a = widths = %v, want [26 26 26]", widths)
	}

	heightsAfter := []int{
		h.jsonPane(after, "pane-2").Position.Height,
		h.jsonPane(after, "pane-4").Position.Height,
		h.jsonPane(after, "pane-5").Position.Height,
	}
	if !reflect.DeepEqual(heightsAfter, heightsBefore) {
		t.Fatalf("Ctrl-a = heights = %v, want unchanged %v", heightsAfter, heightsBefore)
	}
}
