package render

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

func TestRenderPaneStatusUsesXAnsiResetAndCursorPosition(t *testing.T) {
	t.Parallel()

	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 20, 3)
	buf := strings.Builder{}
	renderPaneStatus(&buf, cell, true, &statusPaneData{
		id:    1,
		name:  "pane-1",
		color: config.TextColorHex,
	})

	raw := buf.String()
	if !strings.HasPrefix(raw, ansi.CursorPosition(1, 1)) {
		t.Fatalf("renderPaneStatus() prefix = %q, want prefix %q", raw[:min(8, len(raw))], ansi.CursorPosition(1, 1))
	}
	if !strings.HasSuffix(raw, ansi.ResetStyle) {
		t.Fatalf("renderPaneStatus() suffix = %q, want %q", raw[len(raw)-min(4, len(raw)):], ansi.ResetStyle)
	}
}

func TestRenderCursorDiffUsesXAnsiResetAndOriginCursor(t *testing.T) {
	t.Parallel()

	root := mux.NewLeaf(&mux.Pane{ID: 1, Meta: mux.PaneMeta{Name: "pane-1"}}, 0, 0, 10, 3)
	comp := NewCompositor(10, 4, "test")

	var buf strings.Builder
	comp.renderCursorDiff(&buf, root, 1, func(id uint32) PaneData {
		return &fakePaneData{id: 1, name: "pane-1", screen: "hello"}
	})

	want := ansi.ResetStyle + ansi.CursorPosition(1, 2) + ansi.ShowCursor
	if got := buf.String(); got != want {
		t.Fatalf("renderCursorDiff() = %q, want %q", got, want)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
