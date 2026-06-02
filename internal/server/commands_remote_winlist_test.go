package server

import (
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func TestFormatRemoteWindows(t *testing.T) {
	t.Parallel()

	layout := &proto.LayoutSnapshot{
		ActiveWindowID: 20,
		Windows: []proto.WindowSnapshot{
			{
				ID:    10,
				Name:  "amux",
				Index: 1,
				Root:  proto.CellSnapshot{W: 200, H: 50, Children: []proto.CellSnapshot{leafCell(1, 100, 50), leafCell(2, 100, 50)}},
				Panes: []proto.PaneSnapshot{{ID: 1}, {ID: 2}},
			},
			{
				ID:    20,
				Name:  "orca",
				Index: 2,
				Root:  leafCell(3, 200, 50),
				Panes: []proto.PaneSnapshot{{ID: 3}},
			},
		},
	}

	out := formatRemoteWindows(layout, "hetzner-1", "main")

	for _, want := range []string{"REF", "INDEX", "NAME", "PANES", "amux", "orca", "200x50", "amux://hetzner-1/main/window/index/1", "amux://hetzner-1/main/window/index/2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	// The active window (orca, ID 20) is marked.
	orcaLine := lineContaining(t, out, "orca")
	if !strings.Contains(orcaLine, "*") {
		t.Fatalf("expected active marker on orca line, got %q", orcaLine)
	}
	// amux has 2 panes.
	amuxLine := lineContaining(t, out, "amux")
	if !strings.Contains(amuxLine, "2") {
		t.Fatalf("expected pane count 2 on amux line, got %q", amuxLine)
	}
}

func TestFormatRemoteWindowsEmpty(t *testing.T) {
	t.Parallel()
	if got := formatRemoteWindows(&proto.LayoutSnapshot{}, "hetzner-1", "main"); !strings.Contains(got, "No windows") {
		t.Fatalf("expected empty notice, got %q", got)
	}
}

func lineContaining(t *testing.T, text, sub string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, sub) {
			return line
		}
	}
	t.Fatalf("no line containing %q in:\n%s", sub, text)
	return ""
}
