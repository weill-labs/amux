package render

import (
	"strings"

	"github.com/muesli/termenv"
)

const (
	chooserModalMaxWidth  = 80
	chooserModalMinMargin = 2
	// chooserChromeOverhead is the count of non-list rows the chooser chrome
	// always draws: top + bottom borders, the query line, the two list
	// spacers, and the footer. The visible-row window must leave room for them
	// or computeLayout rejects the box and nothing renders.
	chooserChromeOverhead = 6
)

// chooserRowLimit is the maximum number of list rows that fit on a screen of
// the given height. Both chooserChrome (windowing) and ChooserRowAtPoint
// (hit-testing) use it so clickable geometry can never drift from what is
// drawn.
func chooserRowLimit(screenH int) int {
	return max(screenH-chooserModalMinMargin*2-chooserChromeOverhead, 1)
}

// chooserChrome converts a ChooserOverlay into the shared dialogChrome,
// windowing the rows around the selection so a long list fits on screen and
// driving a scrollbar for the overflow.
func chooserChrome(screenH int, overlay *ChooserOverlay) dialogChrome {
	start, end := chooserVisibleWindow(len(overlay.Rows), overlay.Selected, chooserRowLimit(screenH))
	rows := make([]dialogRow, 0, end-start)
	for i := start; i < end; i++ {
		src := overlay.Rows[i]
		row := dialogRow{text: src.Text, desc: src.Desc, icon: src.Icon, rule: src.Rule, kind: dialogRowNormal}
		if src.IconColor != "" {
			row.iconColor = hexToColor(src.IconColor)
		}
		if src.TextColor != "" {
			row.textColor = hexToColor(src.TextColor)
		}
		switch {
		case i == overlay.Selected && src.Selectable:
			row.kind = dialogRowSelected
		case src.Header:
			row.kind = dialogRowHeader
		case !src.Selectable:
			row.kind = dialogRowDim
		}
		rows = append(rows, row)
	}

	footer := []footerHint{
		{key: "↑/↓", label: "choose"},
		{key: "enter", label: "open"},
		{key: "esc", label: "close"},
	}
	if overlay.Toggle != nil {
		footer = append([]footerHint{{key: "tab", label: "switch"}}, footer...)
	}
	chrome := dialogChrome{
		title:     overlay.Title,
		showQuery: true,
		query:     overlay.Query,
		rows:      rows,
		footer:    footer,
	}
	if overlay.Toggle != nil {
		chrome.toggle = newDialogToggle(overlay.Toggle.Selected, overlay.Toggle.Options...)
	}
	if len(overlay.Rows) > len(rows) {
		chrome.scroll = &dialogScroll{total: len(overlay.Rows), offset: start, visible: len(rows)}
	}
	return chrome
}

// chooserVisibleWindow returns the [start, end) slice of rows to show, centering
// the selection when the full set overflows rowLimit.
func chooserVisibleWindow(count, selected, rowLimit int) (int, int) {
	if count <= rowLimit {
		return 0, count
	}
	start := max(selected-rowLimit/2, 0)
	end := start + rowLimit
	if end > count {
		end = count
		start = end - rowLimit
	}
	return start, end
}

// ChooserRowAtPoint maps a screen point to the chooser modal. inside reports
// whether the point falls within the modal box; onRow and absRow are valid when
// it lands on a selectable list row. It reuses computeLayout so the clickable
// geometry matches exactly what is rendered.
func ChooserRowAtPoint(screenW, screenH int, overlay *ChooserOverlay, x, y int) (absRow int, onRow, inside bool) {
	if overlay == nil {
		return 0, false, false
	}
	chrome := chooserChrome(screenH, overlay)
	rect, bodyTop, ok := chrome.computeLayout(screenW, screenH)
	if !ok {
		return 0, false, false
	}
	if x < rect.X || x >= rect.X+rect.W || y < rect.Y || y >= rect.Y+rect.H {
		return 0, false, false
	}

	start, end := chooserVisibleWindow(len(overlay.Rows), overlay.Selected, chooserRowLimit(screenH))
	idx := y - bodyTop
	if idx < 0 || idx >= end-start {
		return 0, false, true
	}
	absRow = start + idx
	return absRow, overlay.Rows[absRow].Selectable, true
}

func buildChooserOverlayCells(g *ScreenGrid, overlay *ChooserOverlay) {
	if overlay == nil {
		return
	}
	chooserChrome(g.Height, overlay).place(g, defaultDialogStyles())
}

func renderChooserOverlay(buf *strings.Builder, width, height int, overlay *ChooserOverlay) {
	renderChooserOverlayWithProfile(buf, width, height, overlay, defaultColorProfile)
}

func renderChooserOverlayWithProfile(buf *strings.Builder, width, height int, overlay *ChooserOverlay, profile termenv.Profile) {
	if overlay == nil {
		return
	}
	emitDialogChrome(buf, width, height, chooserChrome(height, overlay), profile)
}
