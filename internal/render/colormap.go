package render

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/weill-labs/amux/internal/config"
)

var knownNonBorderColors = map[string]byte{
	config.TextColorHex: '|', // TextFg — global bar separators
}

// ExtractColorMap takes a raw ANSI capture stream and produces a human-readable
// color map. At each border character position, the active ANSI foreground color
// is mapped to a letter (Catppuccin name initial) or '.' for dim.
// Non-border positions are spaces.
func ExtractColorMap(ansiStream string, width, height int) string {
	type cell struct {
		r     rune
		color string // hex color like "f5e0dc"
	}
	grid := make([][]cell, height)
	for i := range grid {
		grid[i] = make([]cell, width)
		for j := range grid[i] {
			grid[i][j] = cell{r: ' '}
		}
	}

	row, col := 0, 0
	currentColor := ""
	i := 0

	for i < len(ansiStream) {
		b := ansiStream[i]

		if b == '\033' && i+1 < len(ansiStream) && ansiStream[i+1] == '[' {
			params, finalByte, end := CSIParams(ansiStream, i+2)
			if finalByte == 'H' || finalByte == 'f' {
				r, c := ParseCUP(params)
				row = Clamp(r-1, 0, height-1)
				col = Clamp(c-1, 0, width-1)
			} else if finalByte == 'm' {
				currentColor = extractFgHex(params, currentColor)
			}
			i = end
			continue
		}
		if b == '\033' {
			i = skipANSISequence(ansiStream, i)
			continue
		}

		if b == '\n' {
			row++
			col = 0
			i++
			continue
		}
		if b == '\r' {
			col = 0
			i++
			continue
		}
		if b < 0x20 {
			i++
			continue
		}

		r, size := utf8.DecodeRuneInString(ansiStream[i:])
		if row >= 0 && row < height && col >= 0 && col < width {
			grid[row][col] = cell{r: r, color: currentColor}
			col++
		}
		i += size
	}

	var buf strings.Builder
	for ri, line := range grid {
		if ri > 0 {
			buf.WriteByte('\n')
		}
		rowStr := make([]byte, width)
		for ci := range rowStr {
			rowStr[ci] = ' '
		}
		for ci, c := range line {
			if isBorderRune(c.r) {
				rowStr[ci] = colorToLetter(c.color)
			}
		}
		buf.WriteString(strings.TrimRight(string(rowStr), " "))
	}
	return buf.String()
}

func extractFgHex(params string, current string) string {
	if params == "0" || params == "" {
		return ""
	}
	parts := strings.Split(params, ";")
	if len(parts) >= 5 && parts[0] == "38" && parts[1] == "2" {
		r, _ := strconv.Atoi(parts[2])
		g, _ := strconv.Atoi(parts[3])
		b, _ := strconv.Atoi(parts[4])
		return fmt.Sprintf("%02x%02x%02x", r, g, b)
	}
	return current
}

func colorToLetter(hex string) byte {
	if hex == "" || hex == config.DimColorHex {
		return '.'
	}
	if l, ok := config.AccentColorLetter(hex); ok {
		return l
	}
	if l, ok := knownNonBorderColors[hex]; ok {
		return l
	}
	return '?'
}

func isBorderRune(r rune) bool {
	switch r {
	case '│', '─', '┼', '├', '┤', '┬', '┴', '┌', '┐', '└', '┘':
		return true
	}
	return false
}
