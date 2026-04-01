package render

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/muesli/termenv"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

// PaneOverlayLabel identifies a temporary overlay label for a pane.
type PaneOverlayLabel struct {
	PaneID uint32
	Label  string
}

func buildPaneOverlayCells(g *ScreenGrid, root *mux.LayoutCell, lookup func(uint32) PaneData, labels []PaneOverlayLabel) {
	for _, label := range labels {
		cell := root.FindByPaneID(label.PaneID)
		if cell == nil || label.Label == "" {
			continue
		}
		badge, x, y := paneOverlayPlacement(cell, label.Label)
		if badge == "" {
			continue
		}
		fg := config.TextColorHex
		if pd := lookup(label.PaneID); pd != nil && pd.Color() != "" {
			fg = pd.Color()
		}
		style := uv.Style{
			Fg:    hexToColor(fg),
			Bg:    hexToColor(config.Surface0Hex),
			Attrs: uv.AttrBold,
		}
		for i, r := range badge {
			g.Set(x+i, y, ScreenCell{
				Char:  string(r),
				Width: 1,
				Style: style,
			})
		}
	}
}

func renderPaneOverlay(buf *strings.Builder, root *mux.LayoutCell, lookup func(uint32) PaneData, labels []PaneOverlayLabel) {
	renderPaneOverlayWithProfile(buf, root, lookup, labels, defaultColorProfile)
}

func renderPaneOverlayWithProfile(buf *strings.Builder, root *mux.LayoutCell, lookup func(uint32) PaneData, labels []PaneOverlayLabel, profile termenv.Profile) {
	surface0Bg := bgHexSequence(config.Surface0Hex, profile)
	for _, label := range labels {
		cell := root.FindByPaneID(label.PaneID)
		if cell == nil || label.Label == "" {
			continue
		}
		badge, x, y := paneOverlayPlacement(cell, label.Label)
		if badge == "" {
			continue
		}
		color := config.TextColorHex
		if pd := lookup(label.PaneID); pd != nil && pd.Color() != "" {
			color = pd.Color()
		}
		writeCursorTo(buf, y+1, x+1)
		buf.WriteString(surface0Bg)
		buf.WriteString(Bold)
		buf.WriteString(hexToANSIWithProfile(color, profile))
		buf.WriteString(badge)
		buf.WriteString(Reset)
	}
}

func paneOverlayPlacement(cell *mux.LayoutCell, label string) (badge string, x, y int) {
	if label == "" || cell == nil || cell.W <= 0 || cell.H <= 0 {
		return "", 0, 0
	}
	if cell.W >= 3 {
		badge = "[" + label + "]"
	} else {
		badge = label[:1]
	}

	x = cell.X + (cell.W-len(badge))/2
	if x < cell.X {
		x = cell.X
	}
	maxX := cell.X + cell.W - len(badge)
	if maxX < cell.X {
		maxX = cell.X
	}
	if x > maxX {
		x = maxX
	}

	contentH := mux.PaneContentHeight(cell.H)
	if contentH > 0 {
		y = cell.Y + mux.StatusLineRows + contentH/2
	} else {
		y = cell.Y
	}
	return badge, x, y
}
