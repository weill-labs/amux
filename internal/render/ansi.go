package render

import "fmt"

// ANSI CSI escape sequences for terminal rendering.
const (
	HideCursor = "\033[?25l"
	ShowCursor = "\033[?25h"
	ClearAll   = "\033[2J"
	CursorHome = "\033[H"
	Reset      = "\033[0m"
	Bold       = "\033[1m"
	NoBold     = "\033[22m"
)

// Catppuccin Mocha ANSI color escapes (catppuccin.com/palette).
const (
	DimFg      = "\033[38;2;108;112;134m" // Overlay 0 (#6c7086)
	Surface0Bg = "\033[48;2;49;50;68m"    // Surface 0 (#313244)
	TextFg     = "\033[38;2;205;214;244m" // Text (#cdd6f4)
	GreenFg    = "\033[38;2;166;227;161m" // Green (#a6e3a1)
	BlueFg     = "\033[38;2;137;180;250m" // Blue (#89b4fa)
	YellowFg   = "\033[38;2;249;226;175m" // Yellow (#f9e2af)
)

// OSC (Operating System Command) sequences for terminal title.
const (
	AltScreenEnter = "\033[?1049h"
	AltScreenExit  = "\033[?1049l"
	ResetTitle     = "\033]0;\007"
)

// SetTitle returns an OSC escape to set the terminal title.
func SetTitle(title string) string {
	return fmt.Sprintf("\033]0;%s\007", title)
}

// CursorTo returns an ANSI escape to move the cursor to (row, col), 1-based.
func CursorTo(row, col int) string {
	return fmt.Sprintf("\033[%d;%dH", row, col)
}
