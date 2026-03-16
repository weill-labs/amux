package client

import (
	"fmt"
	"net"

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

// HandleMouseEvent dispatches a parsed mouse event to the appropriate action:
// click-to-focus, border drag, or scroll wheel.
func HandleMouseEvent(ev mouse.Event, cr *ClientRenderer, conn net.Conn, drag *DragState) {
	layout := cr.Layout()

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
		} else if cell := layout.FindLeafAt(ev.X, ev.Y); cell != nil {
			paneID := cell.CellPaneID()
			alreadyActive := paneID == cr.ActivePaneID()
			if !alreadyActive {
				SendCommand(conn, "focus", []string{fmt.Sprintf("%d", paneID)})
			}
		}

	case ev.Action == mouse.Motion && drag.Active:
		dx := ev.X - ev.LastX
		dy := ev.Y - ev.LastY
		delta := dx
		if drag.BorderDir == mux.SplitVertical {
			delta = dy
		}
		if delta != 0 {
			SendCommand(conn, "resize-border", []string{
				fmt.Sprintf("%d", drag.BorderX),
				fmt.Sprintf("%d", drag.BorderY),
				fmt.Sprintf("%d", delta),
			})
			if drag.BorderDir == mux.SplitHorizontal {
				drag.BorderX += dx
			} else {
				drag.BorderY += dy
			}
		}

	case ev.Action == mouse.Release:
		drag.Active = false

	case ev.Button == mouse.ScrollUp:
		// Scroll wheel sends arrow keys to the active pane
		proto.WriteMsg(conn, &proto.Message{
			Type: proto.MsgTypeInput, Input: []byte("\033[A\033[A\033[A"),
		})
	case ev.Button == mouse.ScrollDown:
		proto.WriteMsg(conn, &proto.Message{
			Type: proto.MsgTypeInput, Input: []byte("\033[B\033[B\033[B"),
		})
	}
}
