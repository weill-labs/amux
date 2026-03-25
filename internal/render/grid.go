package render

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// MaterializeGrid replays an ANSI escape stream onto a 2D character grid
// and returns the result as newline-separated rows. Only CUP (\033[row;colH)
// cursor positioning is handled; other cursor movement (A/B/C/D) is skipped.
// This is sufficient for the compositor's output, which uses CUP exclusively.
func MaterializeGrid(ansiStream string, width, height int) string {
	grid := make([][]rune, height)
	for i := range grid {
		grid[i] = make([]rune, width)
		for j := range grid[i] {
			grid[i][j] = ' '
		}
	}

	row, col := 0, 0 // 0-indexed cursor position
	i := 0

	for i < len(ansiStream) {
		b := ansiStream[i]

		// ESC sequence
		if b == '\033' && i+1 < len(ansiStream) && ansiStream[i+1] == '[' {
			// CSI: extract params to handle CUP positioning
			params, finalByte, end := CSIParams(ansiStream, i+2)
			if finalByte == 'H' || finalByte == 'f' {
				r, c := ParseCUP(params)
				row = Clamp(r-1, 0, height-1)
				col = Clamp(c-1, 0, width-1)
			}
			i = end
			continue
		}
		if b == '\033' {
			i = skipANSISequence(ansiStream, i)
			continue
		}

		// Newline: move to next row
		if b == '\n' {
			row++
			col = 0
			i++
			continue
		}

		// Carriage return: reset column
		if b == '\r' {
			col = 0
			i++
			continue
		}

		// Tab: advance to next 8-col tab stop
		if b == '\t' {
			col = ((col / 8) + 1) * 8
			if col >= width {
				col = width - 1
			}
			i++
			continue
		}

		// Skip other control characters
		if b < 0x20 {
			i++
			continue
		}

		// Decode UTF-8 rune and place on grid using display width so wide
		// characters occupy the same number of columns they do in terminal output.
		r, size := utf8.DecodeRuneInString(ansiStream[i:])
		if row >= 0 && row < height && col >= 0 && col < width {
			grid[row][col] = r
			rw := runewidth.RuneWidth(r)
			if rw <= 0 {
				rw = 1
			}
			col += rw
		}
		i += size
	}

	// Build output: join rows, trim trailing spaces per line
	var buf strings.Builder
	for ri, line := range grid {
		if ri > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(strings.TrimRight(string(line), " "))
	}
	return buf.String()
}

// ParseCUP parses CSI CUP parameters "row;col" (1-based). Missing values default to 1.
func ParseCUP(params string) (row, col int) {
	row, col = 1, 1
	if params == "" || params[0] == '?' {
		return
	}

	parts := strings.SplitN(params, ";", 2)
	if v, err := strconv.Atoi(parts[0]); err == nil && v > 0 {
		row = v
	}
	if len(parts) > 1 {
		if v, err := strconv.Atoi(parts[1]); err == nil && v > 0 {
			col = v
		}
	}
	return
}

// Clamp restricts v to the range [lo, hi].
func Clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
