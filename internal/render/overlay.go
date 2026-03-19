package render

// OverlayState captures optional client-local overlays that sit on top of the
// normal pane layout rendering.
type OverlayState struct {
	PaneLabels []PaneOverlayLabel
	Chooser    *ChooserOverlay
	Message    string
	Message    string
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
