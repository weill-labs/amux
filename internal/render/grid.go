package render

import (
	"strconv"
	"strings"
	"unicode/utf8"
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
		if b == '\033' && i+1 < len(ansiStream) {
			next := ansiStream[i+1]

			// CSI: \033[ ... final_byte
			if next == '[' {
				j := i + 2
				// Collect parameter bytes and intermediate bytes
				for j < len(ansiStream) && ansiStream[j] >= 0x20 && ansiStream[j] <= 0x3F {
					j++
				}
				// Final byte
				if j < len(ansiStream) {
					finalByte := ansiStream[j]
					params := ansiStream[i+2 : j]

					if finalByte == 'H' || finalByte == 'f' {
						// CUP: \033[row;colH (1-based) or \033[H (home)
						r, c := ParseCUP(params)
						row = Clamp(r-1, 0, height-1)
						col = Clamp(c-1, 0, width-1)
					}
					// All other CSI sequences (SGR, clear, cursor visibility) — skip

					i = j + 1
					continue
				}
			}

			// OSC: \033] ... BEL(\007) or ST(\033\\)
			if next == ']' {
				j := i + 2
				for j < len(ansiStream) {
					if ansiStream[j] == '\007' {
						j++
						break
					}
					if ansiStream[j] == '\033' && j+1 < len(ansiStream) && ansiStream[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				i = j
				continue
			}

			// Other ESC sequences (charset designation \033( \033) etc.)
			// Skip ESC + one byte
			i += 2
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

		// Decode UTF-8 rune and place on grid
		r, size := utf8.DecodeRuneInString(ansiStream[i:])
		if row >= 0 && row < height && col >= 0 && col < width {
			grid[row][col] = r
			col++
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
