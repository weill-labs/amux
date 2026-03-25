package client

import (
	"fmt"
	"time"

	"github.com/weill-labs/amux/internal/copymode"
	"github.com/weill-labs/amux/internal/mouse"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

// dragState tracks an in-progress border drag. The border direction is
// cached from the initial press so motion events don't need to re-query
// the layout (which may be stale during fast drags).
type dragState struct {
	Active    bool
	BorderX   int
	BorderY   int
	BorderDir mux.SplitDir

	CopyModeActive bool
	CopyModePaneID uint32
	CopyStartX     int
	CopyStartY     int
	CopyMoved      bool

	LastClickAt time.Time
	LastClickX  int
	LastClickY  int
	ClickCount  int

	PendingWordCopyPaneID uint32
	PendingWordCopyAt     time.Time
}

type paneMouseTarget struct {
	cell      *mux.LayoutCell
	paneID    uint32
	localX    int
	localY    int
	inContent bool
}

const (
	wheelScrollLines      = 5
	mouseMultiClickWindow = 300 * time.Millisecond
	mouseWordCopyDelay    = 300 * time.Millisecond
)

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

func paneAllowsMouseCopySelection(cr *ClientRenderer, paneID uint32) bool {
	emu, ok := cr.Emulator(paneID)
	if !ok {
		return false
	}
	if emu.IsAltScreen() {
		return false
	}
	return !emu.MouseProtocol().Enabled()
}

func globalBarWindowInfos(cr *ClientRenderer) []render.WindowInfo {
	windows, activeWindowID := cr.renderer.WindowSnapshots()
	if len(windows) == 0 {
		return nil
	}

	infos := make([]render.WindowInfo, len(windows))
	for i, ws := range windows {
		infos[i] = render.WindowInfo{
			Index:    ws.Index,
			Name:     ws.Name,
			IsActive: ws.ID == activeWindowID,
			Panes:    len(ws.Panes),
		}
	}
	return infos
}

func handleGlobalBarClick(ev mouse.Event, layout *mux.LayoutCell, cr *ClientRenderer, sender *messageSender) bool {
	if ev.Action != mouse.Press || ev.Button != mouse.ButtonLeft || layout == nil || ev.Y != layout.H {
		return false
	}

	windows := globalBarWindowInfos(cr)
	if len(windows) <= 1 {
		return true
	}

	window, ok := render.GlobalBarWindowAtColumn(windows, ev.X)
	if ok && !window.IsActive && sender != nil {
		sender.Command("select-window", []string{fmt.Sprintf("%d", window.Index)})
	}
	return true
}

// handleMouseEvent dispatches a parsed mouse event to the appropriate action:
// click-to-focus, border drag, or scroll wheel.
func handleMouseEvent(ev mouse.Event, cr *ClientRenderer, sender *messageSender, drag *dragState, msgCh chan<- *RenderMsg) {
	layout := cr.VisibleLayout()

	if layout == nil {
		return
	}
	if handleGlobalBarClick(ev, layout, cr, sender) {
		return
	}

	switch {
	case ev.Action == mouse.Press && ev.Button == mouse.ButtonLeft:
		// Check if clicking on a border (start drag) or a pane (focus)
		if hit := layout.FindBorderAt(ev.X, ev.Y); hit != nil {
			drag.Active = true
			drag.CopyModeActive = false
			drag.CopyModePaneID = 0
			drag.BorderX = ev.X
			drag.BorderY = ev.Y
			drag.BorderDir = hit.Dir
		} else if target := mouseTargetAt(layout, ev.X, ev.Y); target != nil {
			focusPane(sender, target.paneID, cr.ActivePaneID())
			drag.CopyModeActive = false
			drag.CopyModePaneID = 0
			drag.CopyMoved = false
			if cr.InCopyMode(target.paneID) && target.inContent {
				drag.CopyModeActive = true
				drag.CopyModePaneID = target.paneID
				drag.CopyStartX = target.localX
				drag.CopyStartY = target.localY
				drag.CopyMoved = false
				_ = callLocalRenderAction[struct{}](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
					cr.CopyModeSetCursor(target.paneID, target.localX, target.localY)
					return renderNowResult()
				})
			} else if target.inContent && paneAllowsMouseCopySelection(cr, target.paneID) {
				drag.CopyModePaneID = target.paneID
				drag.CopyStartX = target.localX
				drag.CopyStartY = target.localY
			}
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

	case ev.Action == mouse.Motion && drag.CopyModePaneID != 0:
		target := mouseTargetAt(layout, ev.X, ev.Y)
		if target == nil || !target.inContent || target.paneID != drag.CopyModePaneID {
			return
		}
		if !drag.CopyModeActive {
			entered := callLocalRenderAction[bool](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
				cr.EnterCopyMode(drag.CopyModePaneID)
				return localRenderResult{
					effects: renderNowResult().effects,
					value:   cr.InCopyMode(drag.CopyModePaneID),
				}
			})
			if !entered {
				return
			}
			drag.CopyModeActive = true
		}
		startSelection := !drag.CopyMoved
		_ = callLocalRenderAction[struct{}](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
			if startSelection {
				cr.CopyModeSetCursor(drag.CopyModePaneID, drag.CopyStartX, drag.CopyStartY)
				cr.CopyModeStartSelection(drag.CopyModePaneID)
			}
			cr.CopyModeSetCursor(drag.CopyModePaneID, target.localX, target.localY)
			return renderNowResult()
		})
		if startSelection {
			drag.CopyMoved = true
		}

	case ev.Action == mouse.Release:
		drag.Active = false
		if drag.CopyModePaneID != 0 {
			if drag.CopyModeActive {
				if drag.CopyMoved {
					drag.PendingWordCopyPaneID = 0
					drag.PendingWordCopyAt = time.Time{}
					drag.ClickCount = 0
					_ = callLocalRenderAction[struct{}](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
						cr.CopyModeCopySelection(drag.CopyModePaneID)
						return renderNowResult()
					})
				} else if target := mouseTargetAt(layout, ev.X, ev.Y); target != nil && target.inContent && target.paneID == drag.CopyModePaneID {
					now := time.Now()
					if target.localX == drag.LastClickX &&
						target.localY == drag.LastClickY &&
						now.Sub(drag.LastClickAt) <= mouseMultiClickWindow {
						drag.ClickCount++
					} else {
						drag.ClickCount = 1
					}
					drag.LastClickAt = now
					drag.LastClickX = target.localX
					drag.LastClickY = target.localY

					switch drag.ClickCount {
					case 2:
						_ = callLocalRenderAction[struct{}](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
							if cm := cr.CopyModeForPane(target.paneID); cm != nil {
								cr.CopyModeSetCursor(target.paneID, target.localX, target.localY)
								if cm.SelectWord() == copymode.ActionRedraw {
									cr.markDirty()
								}
							}
							return renderNowResult()
						})
						drag.PendingWordCopyPaneID = target.paneID
						drag.PendingWordCopyAt = now.Add(mouseWordCopyDelay)
					case 3:
						drag.PendingWordCopyPaneID = 0
						drag.PendingWordCopyAt = time.Time{}
						_ = callLocalRenderAction[struct{}](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
							if cm := cr.CopyModeForPane(target.paneID); cm != nil {
								cr.CopyModeSetCursor(target.paneID, target.localX, target.localY)
								if cm.SelectLine() == copymode.ActionRedraw {
									cr.markDirty()
								}
							}
							cr.CopyModeCopySelection(target.paneID)
							return renderNowResult()
						})
						drag.ClickCount = 0
					}
				}
			}
			drag.CopyModeActive = false
			drag.CopyModePaneID = 0
			drag.CopyMoved = false
		}

	case ev.Button == mouse.ScrollUp:
		target := mouseTargetAt(layout, ev.X, ev.Y)
		if target == nil {
			return
		}
		if cr.InCopyMode(target.paneID) {
			focusPane(sender, target.paneID, cr.ActivePaneID())
			_ = callLocalRenderAction[struct{}](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
				action := cr.WheelScrollCopyMode(target.paneID, wheelScrollLines, true)
				if action == copymode.ActionNone {
					return localRenderResult{}
				}
				return renderNowResult()
			})
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

		_ = callLocalRenderAction[struct{}](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
			cr.EnterCopyMode(target.paneID)
			if cm := cr.CopyModeForPane(target.paneID); cm != nil {
				cm.SetScrollExit(true)
			}
			cr.WheelScrollCopyMode(target.paneID, wheelScrollLines, true)
			return renderNowResult()
		})

	case ev.Button == mouse.ScrollDown:
		target := mouseTargetAt(layout, ev.X, ev.Y)
		if target == nil {
			return
		}
		if cr.InCopyMode(target.paneID) {
			focusPane(sender, target.paneID, cr.ActivePaneID())
			_ = callLocalRenderAction[struct{}](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
				action := cr.WheelScrollCopyMode(target.paneID, wheelScrollLines, false)
				if action == copymode.ActionNone {
					return localRenderResult{}
				}
				return renderNowResult()
			})
			return
		}
		forwardMouseToPane(cr, sender, target, ev)
	}
}
