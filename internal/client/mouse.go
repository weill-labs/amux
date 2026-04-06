package client

import (
	"fmt"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/copymode"
	"github.com/weill-labs/amux/internal/mouse"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

// dragState tracks in-progress mouse interactions: border drags, pane drags,
// and copy-mode selections. Border direction is cached from the initial press
// so motion events don't need to re-query the layout mid-drag.
type dragState struct {
	Active    bool
	BorderX   int
	BorderY   int
	BorderDir mux.SplitDir

	PaneDragActive         bool
	PaneDragPaneID         uint32
	PaneDropTarget         *paneDropTarget
	WindowTabActive        bool
	WindowDragMoved        bool
	WindowDragSourceActive bool
	WindowDragSourceIndex  int
	WindowDropTarget       *windowTabDropTarget
	CopyModeActive         bool
	CopyModePaneID         uint32
	CopyStartX             int
	CopyStartY             int
	CopyMoved              bool

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

type paneDragCommand struct {
	name string
	args []string
}

type paneDropTarget struct {
	commands  []paneDragCommand
	indicator *render.DropIndicatorOverlay
}

type windowTabDropTarget struct {
	destinationIndex int
	indicator        *render.WindowDropIndicatorOverlay
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

func paneStatusTargetAt(layout *mux.LayoutCell, x, y int) *paneMouseTarget {
	target := mouseTargetAt(layout, x, y)
	if target == nil || target.cell == nil {
		return nil
	}
	if y < target.cell.Y || y >= target.cell.Y+mux.StatusLineRows {
		return nil
	}
	return target
}

func paneRef(paneID uint32) string {
	return fmt.Sprintf("%d", paneID)
}

func logicalRootCell(cr *ClientRenderer, layout *mux.LayoutCell) *mux.LayoutCell {
	if layout == nil {
		return nil
	}
	if layout.IsLeaf() || layout.Dir != mux.SplitVertical || len(layout.Children) < 2 {
		return layout
	}
	lead := layout.Children[0]
	if lead == nil || !lead.IsLeaf() || !paneIsLead(cr, lead.CellPaneID()) {
		return layout
	}
	return layout.Children[1]
}

func paneIsLead(cr *ClientRenderer, paneID uint32) bool {
	info, ok := cr.renderer.PaneInfoSnapshot(paneID)
	return ok && info.Lead
}

const (
	mousePaneCenterMin     = 0.30
	mousePaneCenterMax     = 0.70
	mouseRootEdgeThreshold = 0.05
)

func edgePlacement(edge string) (mux.SplitDir, bool) {
	switch edge {
	case "left":
		return mux.SplitVertical, true
	case "right":
		return mux.SplitVertical, false
	case "top":
		return mux.SplitHorizontal, true
	default:
		return mux.SplitHorizontal, false
	}
}

func normalizedCoord(pos, start, size int) float64 {
	if size <= 0 {
		return 0.5
	}
	return (float64(pos-start) + 0.5) / float64(size)
}

func nearestDropEdge(x, y int, cell *mux.LayoutCell) (string, float64) {
	relX := normalizedCoord(x, cell.X, cell.W)
	relY := normalizedCoord(y, cell.Y, cell.H)
	left := relX
	right := 1 - relX
	top := relY
	bottom := 1 - relY

	edge := "left"
	minDistance := left
	if right < minDistance {
		edge = "right"
		minDistance = right
	}
	if top < minDistance {
		edge = "top"
		minDistance = top
	}
	if bottom < minDistance {
		edge = "bottom"
		minDistance = bottom
	}
	return edge, minDistance
}

func pointInCell(cell *mux.LayoutCell, x, y int) bool {
	return cell != nil &&
		x >= cell.X &&
		x < cell.X+cell.W &&
		y >= cell.Y &&
		y < cell.Y+cell.H
}

func canSplitDrop(cell *mux.LayoutCell, edge string) bool {
	dir, _ := edgePlacement(edge)
	if cell == nil {
		return false
	}
	available := cell.W
	if dir == mux.SplitHorizontal {
		available = cell.H
	}
	return available >= 2*mux.PaneMinSize+1
}

func splitPreviewSizes(size int) (int, int) {
	second := (size - 1) / 2
	first := size - 1 - second
	return first, second
}

func fullPanePreview(cell *mux.LayoutCell) *render.DropIndicatorOverlay {
	if cell == nil {
		return nil
	}
	return &render.DropIndicatorOverlay{
		X: cell.X,
		Y: cell.Y,
		W: cell.W,
		H: cell.H,
	}
}

func splitPanePreview(cell *mux.LayoutCell, edge string) *render.DropIndicatorOverlay {
	if cell == nil {
		return nil
	}
	switch edge {
	case "left":
		firstW, _ := splitPreviewSizes(cell.W)
		return &render.DropIndicatorOverlay{X: cell.X, Y: cell.Y, W: firstW, H: cell.H}
	case "right":
		firstW, secondW := splitPreviewSizes(cell.W)
		return &render.DropIndicatorOverlay{X: cell.X + firstW + 1, Y: cell.Y, W: secondW, H: cell.H}
	case "top":
		firstH, _ := splitPreviewSizes(cell.H)
		return &render.DropIndicatorOverlay{X: cell.X, Y: cell.Y, W: cell.W, H: firstH}
	default:
		firstH, secondH := splitPreviewSizes(cell.H)
		return &render.DropIndicatorOverlay{X: cell.X, Y: cell.Y + firstH + 1, W: cell.W, H: secondH}
	}
}

func resolvePaneDropTarget(cr *ClientRenderer, layout *mux.LayoutCell, sourcePaneID uint32, x, y int) *paneDropTarget {
	if layout == nil || sourcePaneID == 0 || paneIsLead(cr, sourcePaneID) {
		return nil
	}

	root := logicalRootCell(cr, layout)
	if pointInCell(root, x, y) {
		edge, distance := nearestDropEdge(x, y, root)
		if distance <= mouseRootEdgeThreshold && canSplitDrop(root, edge) {
			return &paneDropTarget{
				commands: []paneDragCommand{{
					name: "drop-pane",
					args: []string{paneRef(sourcePaneID), "root", edge},
				}},
				indicator: splitPanePreview(root, edge),
			}
		}
	}

	target := mouseTargetAt(layout, x, y)
	if target == nil || target.paneID == 0 || target.paneID == sourcePaneID {
		return nil
	}
	if paneIsLead(cr, target.paneID) {
		return nil
	}

	relX := normalizedCoord(x, target.cell.X, target.cell.W)
	relY := normalizedCoord(y, target.cell.Y, target.cell.H)
	if relX >= mousePaneCenterMin && relX <= mousePaneCenterMax &&
		relY >= mousePaneCenterMin && relY <= mousePaneCenterMax {
		return &paneDropTarget{
			commands: []paneDragCommand{{
				name: "swap",
				args: []string{paneRef(sourcePaneID), paneRef(target.paneID)},
			}},
			indicator: fullPanePreview(target.cell),
		}
	}

	edge, _ := nearestDropEdge(x, y, target.cell)
	if !canSplitDrop(target.cell, edge) {
		return nil
	}
	return &paneDropTarget{
		commands: []paneDragCommand{{
			name: "drop-pane",
			args: []string{paneRef(sourcePaneID), paneRef(target.paneID), edge},
		}},
		indicator: splitPanePreview(target.cell, edge),
	}
}

func updatePaneDragOverlay(cr *ClientRenderer, drag *dragState) {
	if drag == nil || !drag.PaneDragActive || drag.PaneDragPaneID == 0 {
		cr.hidePaneDragOverlay()
		return
	}

	var indicator *render.DropIndicatorOverlay
	if drag.PaneDropTarget != nil {
		indicator = drag.PaneDropTarget.indicator
	}
	cr.showPaneDragOverlay(drag.PaneDragPaneID, indicator)
}

func clearPaneDragState(cr *ClientRenderer, drag *dragState) {
	if drag == nil {
		cr.hidePaneDragOverlay()
		return
	}
	drag.PaneDragActive = false
	drag.PaneDragPaneID = 0
	drag.PaneDropTarget = nil
	cr.hidePaneDragOverlay()
}

func updateWindowTabDragOverlay(cr *ClientRenderer, drag *dragState) {
	if drag == nil || !drag.WindowTabActive || drag.WindowDropTarget == nil {
		cr.hideWindowTabDragOverlay()
		return
	}
	cr.showWindowTabDragOverlay(drag.WindowDropTarget.indicator)
}

func clearWindowTabDragState(cr *ClientRenderer, drag *dragState) {
	if drag == nil {
		cr.hideWindowTabDragOverlay()
		return
	}
	drag.WindowTabActive = false
	drag.WindowDragMoved = false
	drag.WindowDragSourceActive = false
	drag.WindowDragSourceIndex = 0
	drag.WindowDropTarget = nil
	cr.hideWindowTabDragOverlay()
}

func rerenderOverlay(cr *ClientRenderer, msgCh chan<- *RenderMsg) {
	runLocalRenderAction(cr, msgCh, func(*ClientRenderer) localRenderResult { return overlayRenderNowResult() })
}

func focusPane(sender *messageSender, paneID uint32, activePaneID uint32) {
	if sender == nil || paneID == activePaneID {
		return
	}
	sender.Command("focus", []string{fmt.Sprintf("%d", paneID)})
}

func writePaneInput(sender *messageSender, paneID uint32, data []byte) {
	if sender == nil || len(data) == 0 {
		return
	}
	_ = sender.Send(&proto.Message{
		Type:     proto.MsgTypeInputPane,
		PaneID:   paneID,
		PaneData: data,
	})
}

func forwardMouseToPane(cr *ClientRenderer, sender *messageSender, target *paneMouseTarget, ev mouse.Event) bool {
	if sender == nil || target == nil || !target.inContent {
		return false
	}
	data := cr.renderer.EncodeMouse(target.paneID, ev, target.localX, target.localY)
	if len(data) == 0 {
		return false
	}
	if ev.Action == mouse.Press {
		focusPane(sender, target.paneID, cr.ActivePaneID())
	}
	writePaneInput(sender, target.paneID, data)
	return true
}

func paneWantsMousePassthrough(cr *ClientRenderer, paneID uint32) bool {
	if cr == nil || cr.InCopyMode(paneID) {
		return false
	}
	interaction, ok := cr.renderer.PaneInteractionSnapshot(paneID)
	return ok && interaction.MouseProtocol.Enabled()
}

func forwardMouseEventToApp(cr *ClientRenderer, sender *messageSender, layout *mux.LayoutCell, ev mouse.Event) bool {
	target := mouseTargetAt(layout, ev.X, ev.Y)
	if target == nil || !paneWantsMousePassthrough(cr, target.paneID) {
		return false
	}
	return forwardMouseToPane(cr, sender, target, ev)
}

func paneAllowsMouseCopySelection(cr *ClientRenderer, paneID uint32) bool {
	interaction, ok := cr.renderer.PaneInteractionSnapshot(paneID)
	if !ok {
		return false
	}
	if interaction.AltScreen {
		return false
	}
	return !interaction.MouseProtocol.Enabled()
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

func globalBarMessage(cr *ClientRenderer, snap *rendererSnapshot) string {
	if message := cr.prefixMessage(); message != "" {
		return message
	}
	if snap == nil {
		return ""
	}
	return snap.sessionNotice
}

func globalBarPaneCount(layout *mux.LayoutCell) int {
	if layout == nil {
		return 0
	}
	count := 0
	layout.Walk(func(cell *mux.LayoutCell) {
		if cell.CellPaneID() != 0 {
			count++
		}
	})
	return count
}

func globalBarShowsHelp(cr *ClientRenderer, layout *mux.LayoutCell, windows []render.WindowInfo) bool {
	snap := cr.renderer.loadSnapshot()
	if snap == nil {
		return false
	}
	return render.GlobalBarShowsHelp(
		snap.width,
		snap.sessionName,
		globalBarPaneCount(layout),
		windows,
		globalBarMessage(cr, snap),
		time.Now(),
	)
}

func resolveWindowTabDropTarget(windows []render.WindowInfo, sourceIndex, x int) *windowTabDropTarget {
	target, ok := render.GlobalBarWindowDropTargetAtColumn(windows, sourceIndex, x)
	if !ok {
		return nil
	}
	return &windowTabDropTarget{
		destinationIndex: target.DestinationIndex,
		indicator: &render.WindowDropIndicatorOverlay{
			Column: target.IndicatorColumn,
		},
	}
}

func handleGlobalBarMouseEvent(ev mouse.Event, layout *mux.LayoutCell, cr *ClientRenderer, sender *messageSender, drag *dragState, msgCh chan<- *RenderMsg) bool {
	if layout == nil {
		return false
	}

	snap := cr.renderer.loadSnapshot()
	if snap == nil {
		return false
	}

	isGlobalBarRow := ev.Y == layout.H
	windows := globalBarWindowInfos(cr)
	if drag != nil && drag.WindowTabActive {
		switch ev.Action {
		case mouse.Motion:
			drag.WindowDragMoved = true
			if isGlobalBarRow {
				drag.WindowDropTarget = resolveWindowTabDropTarget(windows, drag.WindowDragSourceIndex, ev.X)
			} else {
				drag.WindowDropTarget = nil
			}
			updateWindowTabDragOverlay(cr, drag)
			rerenderOverlay(cr, msgCh)
			return true
		case mouse.Release:
			sourceIndex := drag.WindowDragSourceIndex
			sourceActive := drag.WindowDragSourceActive
			moved := drag.WindowDragMoved
			target := drag.WindowDropTarget
			clearWindowTabDragState(cr, drag)
			rerenderOverlay(cr, msgCh)
			if target != nil && sender != nil && target.destinationIndex != sourceIndex {
				sender.Command("reorder-window", []string{
					fmt.Sprintf("%d", sourceIndex),
					fmt.Sprintf("%d", target.destinationIndex),
				})
				return true
			}
			if !moved && !sourceActive && isGlobalBarRow && sender != nil {
				if window, ok := render.GlobalBarWindowAtColumn(windows, ev.X); ok && window.Index == sourceIndex {
					sender.Command("select-window", []string{fmt.Sprintf("%d", sourceIndex)})
				}
			}
			return true
		default:
			return true
		}
	}

	if !isGlobalBarRow {
		return false
	}
	if ev.Action != mouse.Press || ev.Button != mouse.ButtonLeft {
		return true
	}

	paneCount := globalBarPaneCount(layout)
	showHelp := globalBarShowsHelp(cr, layout, windows)
	if render.GlobalBarHelpToggleAtColumn(ev.X, snap.width, paneCount, showHelp, time.Now()) {
		toggleHelpBarOnRenderLoop(cr, msgCh, config.DefaultKeybindings())
		return true
	}

	if len(windows) <= 1 {
		return true
	}

	window, ok := render.GlobalBarWindowAtColumn(windows, ev.X)
	if ok {
		clearWindowTabDragState(cr, drag)
		drag.WindowTabActive = true
		drag.WindowDragMoved = false
		drag.WindowDragSourceActive = window.IsActive
		drag.WindowDragSourceIndex = window.Index
	}
	return true
}

// handleMouseEvent dispatches a parsed mouse event to the appropriate action:
// click-to-focus, border drag, or scroll wheel.
func handleMouseEvent(ev mouse.Event, cr *ClientRenderer, sender *messageSender, drag *dragState, msgCh chan<- *RenderMsg) {
	if drag == nil {
		drag = &dragState{}
	}
	layout := cr.VisibleLayout()

	if layout == nil {
		return
	}
	if handleGlobalBarMouseEvent(ev, layout, cr, sender, drag, msgCh) {
		return
	}
	if !drag.Active && !drag.PaneDragActive && forwardMouseEventToApp(cr, sender, layout, ev) {
		drag.Active = false
		clearPaneDragState(cr, drag)
		drag.CopyModeActive = false
		drag.CopyModePaneID = 0
		drag.CopyMoved = false
		return
	}

	switch {
	case ev.Action == mouse.Press && ev.Button == mouse.ButtonLeft:
		// Check if clicking on a border (start drag) or a pane (focus)
		if hit := layout.FindBorderAt(ev.X, ev.Y); hit != nil {
			clearPaneDragState(cr, drag)
			drag.Active = true
			drag.CopyModeActive = false
			drag.CopyModePaneID = 0
			drag.BorderX = ev.X
			drag.BorderY = ev.Y
			drag.BorderDir = hit.Dir
		} else if target := paneStatusTargetAt(layout, ev.X, ev.Y); target != nil {
			focusPane(sender, target.paneID, cr.ActivePaneID())
			drag.Active = false
			drag.CopyModeActive = false
			drag.CopyModePaneID = 0
			drag.CopyMoved = false
			drag.PaneDragActive = !paneIsLead(cr, target.paneID)
			drag.PaneDragPaneID = target.paneID
			drag.PaneDropTarget = nil
			updatePaneDragOverlay(cr, drag)
			rerenderOverlay(cr, msgCh)
		} else if target := mouseTargetAt(layout, ev.X, ev.Y); target != nil {
			clearPaneDragState(cr, drag)
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

	case ev.Action == mouse.Motion && drag.PaneDragActive:
		drag.PaneDropTarget = resolvePaneDropTarget(cr, layout, drag.PaneDragPaneID, ev.X, ev.Y)
		updatePaneDragOverlay(cr, drag)
		rerenderOverlay(cr, msgCh)

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
		if drag.PaneDragActive {
			target := drag.PaneDropTarget
			clearPaneDragState(cr, drag)
			rerenderOverlay(cr, msgCh)
			if target != nil && sender != nil {
				for _, cmd := range target.commands {
					sender.Command(cmd.name, cmd.args)
				}
			}
			return
		}
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

		interaction, ok := cr.renderer.PaneInteractionSnapshot(target.paneID)
		if ok {
			if interaction.AltScreen || interaction.MouseProtocol.Enabled() {
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
