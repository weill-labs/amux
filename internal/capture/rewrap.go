package capture

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/weill-labs/amux/internal/proto"
)

// HistoryLine is one raw history/content row plus the width it was wrapped at.
// SourceWidth is zero when the original wrap width is unknown.
type HistoryLine struct {
	Text        string
	SourceWidth int
	Filled      bool
}

// HistoryBuffer is the transformed history/content payload for capture output.
type HistoryBuffer struct {
	History []string
	Content []string
	Cursor  proto.CaptureCursor
}

// RewrapHistoryBuffer reconstructs logical lines from tracked live rows and
// rewraps them to the requested width. Base history rows are preserved as-is.
func RewrapHistoryBuffer(baseHistory []string, liveHistory, content []HistoryLine, cursor proto.CaptureCursor, targetWidth int) HistoryBuffer {
	out := HistoryBuffer{
		History: append([]string(nil), baseHistory...),
		Cursor:  cursor,
	}
	logicalLines, cursorLogicalIndex, cursorLogicalOffset := buildLogicalBuffer(liveHistory, content, cursor)
	if len(logicalLines) == 0 {
		out.Cursor.Row = 0
		out.Cursor.Col = 0
		return out
	}

	out.Cursor.Row = 0
	for i := 0; i < cursorLogicalIndex; i++ {
		if logicalLines[i].HasContent {
			out.Cursor.Row += wrappedLineCount(logicalLines[i].Text, targetWidth)
		}
	}
	innerRow, innerCol := wrapOffset(cursorLogicalOffset, targetWidth)
	out.Cursor.Row += innerRow
	out.Cursor.Col = innerCol

	for _, line := range logicalLines {
		wrapped := rewrapLogicalLine(line.Text, targetWidth)
		if line.HasContent {
			out.Content = append(out.Content, wrapped...)
			continue
		}
		out.History = append(out.History, wrapped...)
	}
	return out
}

type logicalLine struct {
	Text       string
	HasContent bool
}

func buildLogicalBuffer(liveHistory, content []HistoryLine, cursor proto.CaptureCursor) ([]logicalLine, int, int) {
	rows := make([]HistoryLine, 0, len(liveHistory)+len(content))
	rows = append(rows, liveHistory...)
	rows = append(rows, content...)
	if len(rows) == 0 {
		return nil, 0, 0
	}

	contentStart := len(liveHistory)
	logical := []logicalLine{{Text: rows[0].Text, HasContent: contentStart == 0}}
	logicalIndexes := make([]int, len(rows))
	baseOffsets := make([]int, len(rows))
	for i := 1; i < len(rows); i++ {
		if shouldJoinRows(rows[i-1], rows[i]) {
			logical[len(logical)-1].Text += rows[i].Text
			if i >= contentStart {
				logical[len(logical)-1].HasContent = true
			}
			logicalIndexes[i] = logicalIndexes[i-1]
			baseOffsets[i] = baseOffsets[i-1] + rows[i-1].SourceWidth
			continue
		}
		logical = append(logical, logicalLine{
			Text:       rows[i].Text,
			HasContent: i >= contentStart,
		})
		logicalIndexes[i] = len(logical) - 1
	}

	if len(content) == 0 {
		return logical, 0, 0
	}
	cursorRow := cursor.Row
	if cursorRow < 0 {
		cursorRow = 0
	}
	if cursorRow >= len(content) {
		cursorRow = len(content) - 1
	}
	combinedCursorRow := contentStart + cursorRow
	logicalIndex := logicalIndexes[combinedCursorRow]
	logicalOffset := baseOffsets[combinedCursorRow] + cursor.Col
	if logicalOffset < 0 {
		logicalOffset = 0
	}
	logicalWidth := ansi.StringWidth(logical[logicalIndex].Text)
	if logicalOffset > logicalWidth {
		logicalOffset = logicalWidth
	}
	return logical, logicalIndex, logicalOffset
}

func shouldJoinRows(prev, next HistoryLine) bool {
	return prev.SourceWidth > 0 &&
		prev.Filled &&
		prev.SourceWidth == next.SourceWidth
}

func rewrapLogicalLine(line string, targetWidth int) []string {
	if targetWidth <= 0 {
		return []string{line}
	}
	return strings.Split(ansi.Hardwrap(line, targetWidth, true), "\n")
}

func wrappedLineCount(line string, targetWidth int) int {
	if targetWidth <= 0 {
		return 1
	}
	width := ansi.StringWidth(line)
	if width == 0 {
		return 1
	}
	return (width-1)/targetWidth + 1
}

func wrapOffset(offset, targetWidth int) (row, col int) {
	if targetWidth <= 0 || offset <= 0 {
		return 0, max(offset, 0)
	}
	return (offset - 1) / targetWidth, ((offset - 1) % targetWidth) + 1
}
