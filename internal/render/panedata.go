package render

// PaneData provides the data the compositor needs for rendering a pane.
// Server-side *mux.Pane and client-side emulator+metadata adapters both
// satisfy this interface.
type PaneData interface {
	RenderScreen() string
	CursorPos() (col, row int)
	CursorHidden() bool
	ID() uint32
	Name() string
	Host() string
	Task() string
	Color() string
	Minimized() bool
	InCopyMode() bool
}
