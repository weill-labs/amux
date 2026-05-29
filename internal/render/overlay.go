package render

// OverlayState captures optional client-local overlays that sit on top of the
// normal pane layout rendering.
type OverlayState struct {
	PaneLabels          []PaneOverlayLabel
	DropIndicator       *DropIndicatorOverlay
	WindowDropIndicator *WindowDropIndicatorOverlay
	Chooser             *ChooserOverlay
	TextInput           *TextInputOverlay
	Message             string
	HelpBar             *HelpBarOverlay
	PressedPaneID       uint32
}

// IsPanePressed reports whether the pane with the given ID is currently
// in a pressed/drag state.
func (o OverlayState) IsPanePressed(paneID uint32) bool {
	return o.PressedPaneID != 0 && paneID == o.PressedPaneID
}

// ChooserOverlay is a client-local modal chooser rendered above the layout.
type ChooserOverlay struct {
	Title    string
	Query    string
	Rows     []ChooserOverlayRow
	Selected int
	Toggle   *ChooserToggle // optional Tree/Window mode selector in the title bar
}

// ChooserToggle is the title-bar mode selector (e.g. Tree / Window).
type ChooserToggle struct {
	Options  []string
	Selected int
}

// ChooserOverlayRow is one rendered row in the chooser modal.
type ChooserOverlayRow struct {
	Text       string
	Selectable bool
	Header     bool   // window grouping row in tree mode — rendered bold
	Icon       string // optional leading status glyph
	IconColor  string // hex for the icon (empty → inherit row color)
	TextColor  string // hex for the main text (empty → inherit row color)
	Desc       string // dim trailing metadata (branch, task)
	Rule       bool   // fill trailing width with a horizontal rule (section header)
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

// DropIndicatorOverlay draws a temporary gray placeholder rectangle while a
// pane is being dragged to a new drop target.
type DropIndicatorOverlay struct {
	X, Y int
	W, H int
}

// WindowDropIndicatorOverlay draws a temporary insertion marker in the global
// status bar while a window tab is being dragged.
type WindowDropIndicatorOverlay struct {
	Column int
}
