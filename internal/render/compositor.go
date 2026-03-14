package render

// Compositor composes pane content into terminal output.
// Phase 1: single pane passthrough — raw PTY bytes go directly to client.
// Phase 2: multi-pane compositing with borders from emulator cell state.
type Compositor struct {
	width  int
	height int
}

// NewCompositor creates a compositor for the given terminal dimensions.
func NewCompositor(width, height int) *Compositor {
	return &Compositor{width: width, height: height}
}

// Resize updates the compositor's terminal dimensions.
func (c *Compositor) Resize(width, height int) {
	c.width = width
	c.height = height
}

// ClearScreen returns ANSI sequences to clear the screen and home the cursor.
func ClearScreen() []byte {
	return []byte("\033[2J\033[H")
}
