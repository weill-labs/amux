package test

import (
	"testing"

	"github.com/weill-labs/amux/internal/client"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func TestMultiColumnHeaderLayoutGolden(t *testing.T) {
	t.Parallel()

	const (
		width        = 490
		height       = 87
		sessionName  = "lab-1610"
		scrollback   = mux.DefaultScrollbackLines
		goldenName   = "multi_column_header_layout.golden"
		activePaneID = 869
	)

	cr := client.NewClientRendererWithScrollback(width, height, scrollback)
	cr.HandleLayout(multiColumnHeaderLayoutSnapshot(sessionName, activePaneID))

	resp := cr.HandleCaptureRequest(nil, nil)
	if resp.CmdErr != "" {
		t.Fatalf("capture request failed: %s", resp.CmdErr)
	}

	frame := extractFrame(resp.CmdOutput, sessionName)
	assertGolden(t, goldenName, frame)
}

func multiColumnHeaderLayoutSnapshot(sessionName string, activePaneID uint32) *proto.LayoutSnapshot {
	root := multiColumnHeaderRoot()
	panes := multiColumnHeaderPanes()
	return &proto.LayoutSnapshot{
		SessionName:    sessionName,
		ActivePaneID:   activePaneID,
		Width:          490,
		Height:         86,
		Root:           root,
		Panes:          panes,
		ActiveWindowID: 1,
		Windows: []proto.WindowSnapshot{{
			ID:           1,
			Name:         "alphaos",
			Index:        1,
			ActivePaneID: activePaneID,
			Root:         root,
			Panes:        panes,
		}},
	}
}

func multiColumnHeaderPanes() []proto.PaneSnapshot {
	names := []struct {
		id   uint32
		name string
	}{
		{869, "pane-869"},
		{329, "pane-329"},
		{1056, "w-LAB-1604"},
		{1062, "w-LAB-1608"},
		{1042, "w-LAB-1588"},
		{1058, "w-LAB-1605"},
		{1055, "pane-1055"},
		{1060, "w-LAB-1606"},
		{1059, "pane-1059"},
		{1061, "w-LAB-1607"},
	}
	panes := make([]proto.PaneSnapshot, 0, len(names))
	for i, pane := range names {
		panes = append(panes, proto.PaneSnapshot{
			ID:          pane.id,
			Name:        pane.name,
			Host:        mux.DefaultHost,
			Color:       config.AccentColor(uint32(i)),
			ColumnIndex: multiColumnHeaderColumnIndex(i),
			Idle:        true,
		})
	}
	return panes
}

func multiColumnHeaderColumnIndex(i int) int {
	switch {
	case i == 0:
		return 0
	case i <= 3:
		return 1
	case i <= 5:
		return 2
	case i <= 7:
		return 3
	default:
		return 4
	}
}

func multiColumnHeaderRoot() proto.CellSnapshot {
	return headerSplitSnapshot(mux.SplitVertical, 0, 0, 490, 86,
		headerLeafSnapshot(869, 0, 0, 97, 86),
		headerSplitSnapshot(mux.SplitHorizontal, 98, 0, 97, 86,
			headerLeafSnapshot(329, 98, 0, 97, 28),
			headerLeafSnapshot(1056, 98, 29, 97, 28),
			headerLeafSnapshot(1062, 98, 58, 97, 28),
		),
		headerSplitSnapshot(mux.SplitHorizontal, 196, 0, 97, 86,
			headerLeafSnapshot(1042, 196, 0, 97, 42),
			headerLeafSnapshot(1058, 196, 43, 97, 43),
		),
		headerSplitSnapshot(mux.SplitHorizontal, 294, 0, 97, 86,
			headerLeafSnapshot(1055, 294, 0, 97, 42),
			headerLeafSnapshot(1060, 294, 43, 97, 43),
		),
		headerSplitSnapshot(mux.SplitHorizontal, 392, 0, 98, 86,
			headerLeafSnapshot(1059, 392, 0, 98, 42),
			headerLeafSnapshot(1061, 392, 43, 98, 43),
		),
	)
}

func headerSplitSnapshot(dir mux.SplitDir, x, y, w, h int, children ...proto.CellSnapshot) proto.CellSnapshot {
	return proto.CellSnapshot{
		X:        x,
		Y:        y,
		W:        w,
		H:        h,
		IsLeaf:   false,
		Dir:      int(dir),
		Children: children,
	}
}

func headerLeafSnapshot(paneID uint32, x, y, w, h int) proto.CellSnapshot {
	return proto.CellSnapshot{
		X:      x,
		Y:      y,
		W:      w,
		H:      h,
		IsLeaf: true,
		Dir:    -1,
		PaneID: paneID,
	}
}
