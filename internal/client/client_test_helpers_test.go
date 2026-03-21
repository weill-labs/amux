package client

import (
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func NewClientRenderer(width, height int) *ClientRenderer {
	return NewClientRendererWithScrollback(width, height, mux.DefaultScrollbackLines)
}

func (cr *ClientRenderer) HandleLayout(snap *proto.LayoutSnapshot) bool {
	structureChanged, result := cr.handleLayoutResult(snap)
	cr.emitUIEvents(result.uiEvents)
	return structureChanged
}

func (cr *ClientRenderer) Capture(stripANSI bool) string {
	return cr.renderer.Capture(stripANSI)
}

func (cr *ClientRenderer) CaptureDisplay() string {
	return cr.renderer.CaptureDisplay()
}

func (cr *ClientRenderer) CaptureColorMap() string {
	return cr.renderer.CaptureColorMap()
}

func (cr *ClientRenderer) CapturePaneText(paneID uint32, includeANSI bool) string {
	return cr.renderer.CapturePaneText(paneID, includeANSI)
}
