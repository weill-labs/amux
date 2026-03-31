package proto

import uv "github.com/charmbracelet/ultraviolet"

// Cell is a frozen viewport cell independent of the render package.
type Cell struct {
	Char  string
	Style uv.Style
	Width int
}

type CursorPosition struct {
	Col int
	Row int
}

type SelectionMode uint8

const (
	SelectionModeCharacter SelectionMode = iota
	SelectionModeLine
	SelectionModeRectangle
)

type SelectionRange struct {
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
	Mode      SelectionMode
}

type HighlightKind uint8

const (
	HighlightSelection HighlightKind = iota + 1
	HighlightSearchMatch
	HighlightCurrentMatch
)

type HighlightSpan struct {
	StartCol int
	EndCol   int
	Kind     HighlightKind
}

type HighlightLine struct {
	Row   int
	Spans []HighlightSpan
}

type ViewportOverlay struct {
	Cursor           CursorPosition
	Selection        *SelectionRange
	HighlightedLines []HighlightLine
}
