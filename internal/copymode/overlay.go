package copymode

import (
	"sort"

	uv "github.com/charmbracelet/ultraviolet"
)

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

func (cm *CopyMode) ViewportOverlay() *ViewportOverlay {
	overlay := &ViewportOverlay{
		Cursor: CursorPosition{Col: cm.cx, Row: cm.cy},
	}

	builder := newHighlightBuilder()
	overlay.Selection = cm.selectionRange(builder)
	cm.addMatchHighlights(builder)
	overlay.HighlightedLines = builder.build()
	return overlay
}

type highlightBuilder struct {
	spansByRow map[int][]HighlightSpan
}

func newHighlightBuilder() *highlightBuilder {
	return &highlightBuilder{spansByRow: make(map[int][]HighlightSpan)}
}

func (b *highlightBuilder) add(row, startCol, endCol int, kind HighlightKind) {
	if endCol <= startCol {
		return
	}
	b.spansByRow[row] = append(b.spansByRow[row], HighlightSpan{
		StartCol: startCol,
		EndCol:   endCol,
		Kind:     kind,
	})
}

func (b *highlightBuilder) build() []HighlightLine {
	if len(b.spansByRow) == 0 {
		return nil
	}
	rows := make([]int, 0, len(b.spansByRow))
	for row := range b.spansByRow {
		rows = append(rows, row)
	}
	sort.Ints(rows)

	lines := make([]HighlightLine, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, HighlightLine{
			Row:   row,
			Spans: append([]HighlightSpan(nil), b.spansByRow[row]...),
		})
	}
	return lines
}

func (cm *CopyMode) selectionRange(builder *highlightBuilder) *SelectionRange {
	if !cm.selecting {
		return nil
	}

	startY, startX, endY, endX := cm.normalizedSelection()
	mode := SelectionModeCharacter
	if cm.lineSelect {
		mode = SelectionModeLine
	} else if cm.rectSelect {
		mode = SelectionModeRectangle
	}

	selection := &SelectionRange{
		StartLine: startY,
		StartCol:  startX,
		EndLine:   endY,
		EndCol:    endX,
		Mode:      mode,
	}

	firstVisible := cm.FirstVisibleLine()
	lastVisible := firstVisible + cm.height - 1
	visibleStart := max(startY, firstVisible)
	visibleEnd := min(endY, lastVisible)
	for absIdx := visibleStart; absIdx <= visibleEnd; absIdx++ {
		colStart, colEnd := 0, cm.width
		switch {
		case cm.rectSelect:
			colStart, colEnd = startX, endX+1
		default:
			if absIdx == startY {
				colStart = startX
			}
			if absIdx == endY {
				colEnd = endX + 1
			}
		}
		builder.add(absIdx-firstVisible, colStart, colEnd, HighlightSelection)
	}

	return selection
}

func (cm *CopyMode) addMatchHighlights(builder *highlightBuilder) {
	firstVisible := cm.FirstVisibleLine()
	lastVisible := firstVisible + cm.height - 1
	for i, match := range cm.matches {
		if match.LineIdx < firstVisible || match.LineIdx > lastVisible {
			continue
		}

		kind := HighlightSearchMatch
		if i == cm.matchIdx {
			kind = HighlightCurrentMatch
		}
		builder.add(match.LineIdx-firstVisible, match.Col, match.Col+match.Len, kind)
	}
}
