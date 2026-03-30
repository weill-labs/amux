package test

import (
	"strings"
	"testing"
)

func TestResizePaneCLI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		split     string // "v" or "h"
		focus     string // pane to focus before resize (empty = default)
		target    string // pane to resize
		direction string
		delta     string // empty = omit (default 1)
		wantGrow  string // "width" or "height"
	}{
		{
			name:      "grow right",
			split:     "v",
			target:    "pane-1",
			direction: "right",
			wantGrow:  "width",
		},
		{
			name:      "grow down",
			split:     "h",
			target:    "pane-1",
			direction: "down",
			wantGrow:  "height",
		},
		{
			name:      "custom delta",
			split:     "v",
			target:    "pane-1",
			direction: "right",
			delta:     "10",
			wantGrow:  "width",
		},
		{
			name:      "non-active pane",
			split:     "v",
			focus:     "pane-2",
			target:    "pane-1",
			direction: "right",
			wantGrow:  "width",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newServerHarness(t)

			if tt.split == "v" {
				h.splitV()
			} else {
				h.splitH()
			}

			if tt.focus != "" {
				h.doFocus(tt.focus)
			}

			c := h.captureJSON()
			before := h.jsonPane(c, tt.target)

			args := []string{"resize-pane", tt.target, tt.direction}
			if tt.delta != "" {
				args = append(args, tt.delta)
			}
			out := h.runCmd(args...)
			if !strings.Contains(out, "Resized") {
				t.Fatalf("expected Resized confirmation, got: %s", out)
			}

			c = h.captureJSON()
			after := h.jsonPane(c, tt.target)

			switch tt.wantGrow {
			case "width":
				if after.Position.Width <= before.Position.Width {
					t.Errorf("width should grow: before=%d after=%d",
						before.Position.Width, after.Position.Width)
				}
			case "height":
				if after.Position.Height <= before.Position.Height {
					t.Errorf("height should grow: before=%d after=%d",
						before.Position.Height, after.Position.Height)
				}
			}
		})
	}
}

func TestResizePaneSinglePane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)
	h.unsetLead()

	// Single pane — resize is a no-op, should not error
	out := h.runCmd("resize-pane", "pane-1", "right")
	if !strings.Contains(out, "Resized") {
		t.Fatalf("expected Resized confirmation even for no-op, got: %s", out)
	}
}

func TestResizePaneNotFound(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("resize-pane", "bogus", "right")
	if !strings.Contains(out, "not found") {
		t.Fatalf("expected not found error, got: %s", out)
	}
}

func TestResizePaneInvalidDirection(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	out := h.runCmd("resize-pane", "pane-1", "diagonal")
	if !strings.Contains(out, "invalid direction") {
		t.Fatalf("expected invalid direction error, got: %s", out)
	}
}

func TestResizePaneClampAtMinSize(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	// Try to resize by a huge delta — should clamp at PaneMinSize
	out := h.runCmd("resize-pane", "pane-1", "right", "9999")
	if !strings.Contains(out, "Resized") {
		t.Fatalf("expected Resized confirmation, got: %s", out)
	}

	// pane-2 should still exist (not crushed to zero)
	c := h.captureJSON()
	p2 := h.jsonPane(c, "pane-2")
	if p2.Position.Width < 2 {
		t.Errorf("pane-2 width should be clamped at min size, got %d", p2.Position.Width)
	}
}

func TestResizePaneDefaultDelta(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	c := h.captureJSON()
	before := h.jsonPane(c, "pane-1")

	h.runCmd("resize-pane", "pane-1", "right")

	c = h.captureJSON()
	after := h.jsonPane(c, "pane-1")

	if after.Position.Width != before.Position.Width+1 {
		t.Errorf("default delta should be 1: before=%d after=%d",
			before.Position.Width, after.Position.Width)
	}
}
