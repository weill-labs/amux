package copymode

import (
	"sort"

	"github.com/weill-labs/amux/internal/proto"
)

func (cm *CopyMode) ViewportOverlay() *proto.ViewportOverlay {
	overlay := &proto.ViewportOverlay{
		Cursor: proto.CursorPosition{Col: cm.cx, Row: cm.cy},
	}

	builder := newHighlightBuilder()
	overlay.Selection = cm.selectionRange(builder)
	cm.addMatchHighlights(builder)
	overlay.HighlightedLines = builder.build()
	return overlay
}

type highlightBuilder struct {
	spansByRow map[int][]proto.HighlightSpan
}

func newHighlightBuilder() *highlightBuilder {
	return &highlightBuilder{spansByRow: make(map[int][]proto.HighlightSpan)}
}

func (b *highlightBuilder) add(row, startCol, endCol int, kind proto.HighlightKind) {
	if endCol <= startCol {
		return
	}
	b.spansByRow[row] = append(b.spansByRow[row], proto.HighlightSpan{
		StartCol: startCol,
		EndCol:   endCol,
		Kind:     kind,
	})
}

func (b *highlightBuilder) build() []proto.HighlightLine {
	if len(b.spansByRow) == 0 {
		return nil
	}
	rows := make([]int, 0, len(b.spansByRow))
	for row := range b.spansByRow {
		rows = append(rows, row)
	}
	sort.Ints(rows)

	lines := make([]proto.HighlightLine, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, proto.HighlightLine{
			Row:   row,
			Spans: append([]proto.HighlightSpan(nil), b.spansByRow[row]...),
		})
	}
	return lines
}

func (cm *CopyMode) selectionRange(builder *highlightBuilder) *proto.SelectionRange {
	if !cm.selecting {
		return nil
	}

	startY, startX, endY, endX := cm.normalizedSelection()
	mode := proto.SelectionModeCharacter
	if cm.lineSelect {
		mode = proto.SelectionModeLine
	} else if cm.rectSelect {
		mode = proto.SelectionModeRectangle
	}

	selection := &proto.SelectionRange{
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
		builder.add(absIdx-firstVisible, colStart, colEnd, proto.HighlightSelection)
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

		kind := proto.HighlightSearchMatch
		if i == cm.matchIdx {
			kind = proto.HighlightCurrentMatch
		}
		builder.add(match.LineIdx-firstVisible, match.Col, match.Col+match.Len, kind)
	}
}
