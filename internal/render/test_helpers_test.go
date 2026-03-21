package render

import "github.com/weill-labs/amux/internal/mux"

func (c *Compositor) RenderFull(root *mux.LayoutCell, activePaneID uint32, lookup func(uint32) PaneData, clearScreen ...bool) string {
	return c.RenderFullWithOverlay(root, activePaneID, lookup, OverlayState{}, clearScreen...)
}

func (c *Compositor) RenderDiff(root *mux.LayoutCell, activePaneID uint32, lookup func(uint32) PaneData) string {
	return c.RenderDiffWithOverlay(root, activePaneID, lookup, OverlayState{})
}

func (c *Compositor) LastGrid() *ScreenGrid {
	return c.prevGrid
}

func (g *ScreenGrid) OOBWrites() []OOBWrite {
	return g.oobWrites
}
