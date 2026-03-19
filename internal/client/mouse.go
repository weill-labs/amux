package client

import (
	"fmt"

	"github.com/weill-labs/amux/internal/mouse"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

// DragState tracks an in-progress border drag. The border direction is
// cached from the initial press so motion events don't need to re-query
// the layout (which may be stale during fast drags).
type DragState struct {
	Active    bool
	BorderX   int
	BorderY   int
	BorderDir mux.SplitDir
}

type paneMouseTarget struct {
	cell      *mux.LayoutCell
	paneID    uint32
	localX    int
	localY    int
	inContent bool
}

const wheelScrollLines = 5

func mouseTargetAt(layout *mux.LayoutCell, x, y int) *paneMouseTarget {
	if layout == nil {
		return nil
	}
	cell := layout.FindLeafAt(x, y)
	if cell == nil {
		return nil
	}
	localX := x - cell.X
	localY := y - cell.Y - mux.StatusLineRows
	return &paneMouseTarget{
		cell:      cell,
		paneID:    cell.CellPaneID(),
		localX:    localX,
		localY:    localY,
		inContent: localY >= 0 && localY < mux.PaneContentHeight(cell.H),
	}
}

func focusPane(sender *messageSender, paneID uint32, activePaneID uint32) {
	if paneID == activePaneID {
		return
	}
	sender.Command("focus", []string{fmt.Sprintf("%d", paneID)})
}

func writePaneInput(sender *messageSender, paneID uint32, data []byte) {
	if len(data) == 0 {
		return
	}
	_ = sender.Send(&proto.Message{
		Type:     proto.MsgTypeInputPane,
		PaneID:   paneID,
		PaneData: data,
	})
}

func forwardMouseToPane(cr *ClientRenderer, sender *messageSender, target *paneMouseTarget, ev mouse.Event) bool {
	if target == nil || !target.inContent {
		return false
	}
	emu, ok := cr.Emulator(target.paneID)
	if !ok {
		return false
	}
	data := emu.EncodeMouse(ev, target.localX, target.localY)
	if len(data) == 0 {
		return false
	}
	writePaneInput(sender, target.paneID, data)
	return true
}

// HandleMouseEvent dispatches a parsed mouse event to the appropriate action:
// click-to-focus, border drag, or scroll wheel.
func HandleMouseEvent(ev mouse.Event, cr *ClientRenderer, sender *messageSender, drag *DragState) {
	layout := cr.VisibleLayout()

	if layout == nil {
		return
	}

	switch {
	case ev.Action == mouse.Press && ev.Button == mouse.ButtonLeft:
		// Check if clicking on a border (start drag) or a pane (focus)
		if hit := layout.FindBorderAt(ev.X, ev.Y); hit != nil {
			drag.Active = true
			drag.BorderX = ev.X
			drag.BorderY = ev.Y
			drag.BorderDir = hit.Dir
		} else if target := mouseTargetAt(layout, ev.X, ev.Y); target != nil {
			focusPane(sender, target.paneID, cr.ActivePaneID())
		}

	case ev.Action == mouse.Motion && drag.Active:
		dx := ev.X - ev.LastX
		dy := ev.Y - ev.LastY
		delta := dx
		if drag.BorderDir == mux.SplitHorizontal {
			delta = dy
		}
		if delta != 0 {
			sender.Command("resize-border", []string{
				fmt.Sprintf("%d", drag.BorderX),
				fmt.Sprintf("%d", drag.BorderY),
				fmt.Sprintf("%d", delta),
			})
			if drag.BorderDir == mux.SplitVertical {
				drag.BorderX += dx
			} else {
				drag.BorderY += dy
			}
		}

	case ev.Action == mouse.Release:
		drag.Active = false

	case ev.Button == mouse.ScrollUp:
		target := mouseTargetAt(layout, ev.X, ev.Y)
		if target == nil {
			return
		}
		if cr.InCopyMode(target.paneID) {
			focusPane(sender, target.paneID, cr.ActivePaneID())
			cr.WheelScrollCopyMode(target.paneID, wheelScrollLines, true)
			return
		}

		emu, ok := cr.Emulator(target.paneID)
		if ok {
			protocol := emu.MouseProtocol()
			if emu.IsAltScreen() || protocol.Enabled() {
				forwardMouseToPane(cr, sender, target, ev)
				return
			}
		}

		cr.EnterCopyMode(target.paneID)
		if cm := cr.CopyModeForPane(target.paneID); cm != nil {
			cm.SetScrollExit(true)
		}
		cr.WheelScrollCopyMode(target.paneID, wheelScrollLines, true)

	case ev.Button == mouse.ScrollDown:
		target := mouseTargetAt(layout, ev.X, ev.Y)
		if target == nil {
			return
		}
		if cr.InCopyMode(target.paneID) {
			focusPane(sender, target.paneID, cr.ActivePaneID())
			cr.WheelScrollCopyMode(target.paneID, wheelScrollLines, false)
			return
		}
		forwardMouseToPane(cr, sender, target, ev)
	}
}
