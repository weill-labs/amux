package render

import "github.com/weill-labs/amux/internal/mux"

// OverlayState captures optional client-local overlays that sit on top of the
// normal pane layout rendering.
type OverlayState struct {
	PaneLabels    []PaneOverlayLabel
	DropIndicator *DropIndicatorOverlay
	Chooser       *ChooserOverlay
	TextInput     *TextInputOverlay
	Message       string
	HelpBar       *HelpBarOverlay
}

// ChooserOverlay is a client-local modal chooser rendered above the layout.
type ChooserOverlay struct {
	Title    string
	Query    string
	Rows     []ChooserOverlayRow
	Selected int
}

// ChooserOverlayRow is one rendered row in the chooser modal.
type ChooserOverlayRow struct {
	Text       string
	Selectable bool
}

// TextInputOverlay is a client-local modal prompt rendered above the layout.
type TextInputOverlay struct {
	Title string
	Input string
}

// HelpBarOverlay is a client-local multi-row help footer rendered above the
// global session bar.
type HelpBarOverlay struct {
	Rows []string
}

// DropIndicatorOverlay draws a temporary insertion line while a pane is being
// dragged to a new drop target.
type DropIndicatorOverlay struct {
	X, Y   int
	Length int
	Dir    mux.SplitDir
}
