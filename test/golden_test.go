package test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/weill-labs/amux/internal/render"
)

var updateGoldens = flag.Bool("update", false, "update golden files")

// assertGolden compares actual against a golden file in testdata/.
// With -update flag, writes actual as the new golden.
func assertGolden(t *testing.T, name string, actual string) {
	t.Helper()
	path := filepath.Join("testdata", name)

	if *updateGoldens {
		os.MkdirAll("testdata", 0755)
		if err := os.WriteFile(path, []byte(actual), 0644); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
		t.Logf("updated golden: %s", path)
		return
	}

	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v\nRun with -update to create it.\nActual:\n%s", path, err, actual)
	}

	if actual != string(expected) {
		t.Errorf("golden mismatch: %s\n\n--- expected ---\n%s\n--- actual ---\n%s\n--- diff ---\n%s",
			path, string(expected), actual, goldenDiff(string(expected), actual))
	}
}

// goldenDiff produces a simple line-by-line diff between expected and actual.
func goldenDiff(expected, actual string) string {
	expLines := strings.Split(expected, "\n")
	actLines := strings.Split(actual, "\n")

	maxLen := len(expLines)
	if len(actLines) > maxLen {
		maxLen = len(actLines)
	}

	var diffs []string
	for i := 0; i < maxLen; i++ {
		var exp, act string
		if i < len(expLines) {
			exp = expLines[i]
		}
		if i < len(actLines) {
			act = actLines[i]
		}
		if exp != act {
			diffs = append(diffs, fmt.Sprintf("  line %2d exp: %q", i, exp))
			diffs = append(diffs, fmt.Sprintf("  line %2d act: %q", i, act))
		}
	}
	return strings.Join(diffs, "\n")
}

// ---------------------------------------------------------------------------
// Frame extraction — structural skeleton from plain-text capture
// ---------------------------------------------------------------------------

// extractFrame takes a plain-text capture (from `amux capture`) and returns
// only the structural elements: status lines, borders, and global bar.
// Pane content cells are replaced with spaces. Session name and timestamp
// in the global bar are normalized for deterministic comparison.
func extractFrame(capture string, sessionName string) string {
	lines := strings.Split(capture, "\n")
	var result []string

	for _, line := range lines {
		switch {
		case isStatusLine(line):
			result = append(result, line)
		case isGlobalBar(line):
			result = append(result, normalizeGlobalBar(line, sessionName))
		default:
			// Keep only border characters, replace content with spaces
			result = append(result, extractBorderLine(line))
		}
	}

	return strings.Join(result, "\n")
}

// isStatusLine returns true if the line contains a pane status indicator.
func isStatusLine(line string) bool {
	return strings.Contains(line, "[pane-")
}

var timeRe = regexp.MustCompile(`\d{2}:\d{2}`)

// normalizeGlobalBar replaces the random session name with SESSION and
// the timestamp with 00:00.
func normalizeGlobalBar(line string, sessionName string) string {
	line = strings.ReplaceAll(line, sessionName, "SESSION")
	return timeRe.ReplaceAllString(line, "00:00")
}

// extractBorderLine keeps only box-drawing border characters in a line,
// replacing everything else with spaces. Trailing spaces are trimmed.
func extractBorderLine(line string) string {
	runes := []rune(line)
	out := make([]rune, len(runes))
	for i, r := range runes {
		if isBorderRune(r) {
			out[i] = r
		} else {
			out[i] = ' '
		}
	}
	return strings.TrimRight(string(out), " ")
}

// ---------------------------------------------------------------------------
// Color map extraction — border coloring from ANSI capture
// ---------------------------------------------------------------------------

// Catppuccin Mocha hex → letter mapping.
var catppuccinLetter = map[string]byte{
	"f5e0dc": 'R', // Rosewater
	"f2cdcd": 'F', // Flamingo
	"f5c2e7": 'P', // Pink
	"cba6f7": 'M', // Mauve
	"f38ba8": 'E', // Red
	"eba0ac": 'A', // Maroon
	"fab387": 'H', // Peach
	"f9e2af": 'Y', // Yellow
	"a6e3a1": 'G', // Green
	"94e2d5": 'T', // Teal
	"89dceb": 'S', // Sky
	"74c7ec": 'B', // Sapphire (B for Blue-ish, since Blue is U)
	"89b4fa": 'U', // Blue
	"b4befe": 'L', // Lavender
}

// dimColorHex is the DimFg color used for inactive borders.
const dimColorHex = "6c7086"

// Known non-border colors that appear at box-drawing positions (e.g., global bar │ separators).
var knownNonBorderColors = map[string]byte{
	"cdd6f4": '|', // TextFg — global bar separators
}

// extractColorMap takes a raw ANSI capture stream and produces a human-readable
// color map. At each border character position, the active ANSI foreground color
// is mapped to a letter (Catppuccin name initial) or '.' for dim.
// Non-border positions are spaces.
func extractColorMap(ansiStream string, width, height int) string {
	// Build a grid of (rune, color) by replaying the ANSI stream
	type cell struct {
		r     rune
		color string // hex color like "f5e0dc", or "" for default
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

		if b == '\033' && i+1 < len(ansiStream) {
			next := ansiStream[i+1]

			if next == '[' {
				j := i + 2
				for j < len(ansiStream) && ansiStream[j] >= 0x20 && ansiStream[j] <= 0x3F {
					j++
				}
				if j < len(ansiStream) {
					finalByte := ansiStream[j]
					params := ansiStream[i+2 : j]

					if finalByte == 'H' || finalByte == 'f' {
						r, c := render.ParseCUP(params)
						row = render.Clamp(r-1, 0, height-1)
						col = render.Clamp(c-1, 0, width-1)
					} else if finalByte == 'm' {
						currentColor = extractFgHex(params, currentColor)
					}
					i = j + 1
					continue
				}
			}

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

			i += 2
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

	// Build color map: at border positions, map color to letter
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

// extractFgHex parses SGR parameters and returns the current foreground hex color.
// Handles: "0" (reset), "38;2;R;G;B" (truecolor fg). Returns previous color
// for unrecognized sequences.
func extractFgHex(params string, current string) string {
	if params == "0" || params == "" {
		return ""
	}
	parts := strings.Split(params, ";")
	// Truecolor foreground: 38;2;R;G;B
	if len(parts) >= 5 && parts[0] == "38" && parts[1] == "2" {
		r, _ := strconv.Atoi(parts[2])
		g, _ := strconv.Atoi(parts[3])
		b, _ := strconv.Atoi(parts[4])
		return fmt.Sprintf("%02x%02x%02x", r, g, b)
	}
	return current
}

// colorToLetter maps a hex color to its Catppuccin letter, '.' for dim,
// '|' for global bar separators, or '?' for unknown.
func colorToLetter(hex string) byte {
	if hex == dimColorHex {
		return '.'
	}
	if l, ok := catppuccinLetter[hex]; ok {
		return l
	}
	if l, ok := knownNonBorderColors[hex]; ok {
		return l
	}
	if hex == "" {
		return '.'
	}
	return '?'
}

