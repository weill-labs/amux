package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

// GlobalBarHeight is the number of rows reserved for the global status bar.
const GlobalBarHeight = 1

// renderPaneStatus draws a per-pane status line at the top of a pane cell.
// Format: ● [name] @host task
func renderPaneStatus(buf *strings.Builder, cell *mux.LayoutCell, isActive bool) {
	if cell.Pane == nil {
		return
	}
	meta := cell.Pane.Meta

	// Move to cell position
	buf.WriteString(CursorTo(cell.Y+1, cell.X+1))

	// Background: subtle dark surface
	buf.WriteString(Surface0Bg)

	// Status icon with pane color
	if isActive {
		buf.WriteString(hexToANSI(meta.Color))
		buf.WriteString("●")
	} else {
		buf.WriteString(DimFg)
		buf.WriteString("○")
	}

	// Name in bold with pane color
	buf.WriteString(" ")
	if isActive {
		buf.WriteString(Bold)
		buf.WriteString(hexToANSI(meta.Color))
	} else {
		buf.WriteString(TextFg)
	}
	buf.WriteString(fmt.Sprintf("[%s]", meta.Name))
	buf.WriteString(NoBold)

	// Host (only if not "local")
	if meta.Host != "" && meta.Host != "local" {
		buf.WriteString(GreenFg)
		buf.WriteString(fmt.Sprintf(" @%s", meta.Host))
	}

	// Task
	if meta.Task != "" {
		buf.WriteString(TextFg)
		buf.WriteString(fmt.Sprintf(" %s", meta.Task))
	}

	// Fill remaining width with spaces
	// Calculate how many chars we've written (rough estimate)
	usedWidth := 2 + len(meta.Name) + 2 // "● [name]"
	if meta.Host != "" && meta.Host != "local" {
		usedWidth += 2 + len(meta.Host)
	}
	if meta.Task != "" {
		usedWidth += 1 + len(meta.Task)
	}
	remaining := cell.W - usedWidth
	if remaining > 0 {
		buf.WriteString(strings.Repeat(" ", remaining))
	}

	buf.WriteString(Reset)
}

// renderGlobalBar draws the global status bar at the bottom of the terminal.
func renderGlobalBar(buf *strings.Builder, sessionName string, paneCount int, width, yPos int) {
	buf.WriteString(CursorTo(yPos+1, 1))

	// Catppuccin surface0 bg, text fg
	buf.WriteString(Surface0Bg + TextFg)

	now := time.Now().Format("15:04")

	left := " " + Bold + "amux" + NoBold + " │ " + sessionName + " "
	right := fmt.Sprintf(" %d panes │ %s ", paneCount, now)

	buf.WriteString(left)

	// Fill middle
	// Approximate visible width (ignore ANSI escapes for rough fill)
	leftVisible := 8 + len(sessionName) // " amux │ session "
	rightVisible := len(right)
	fill := width - leftVisible - rightVisible
	if fill > 0 {
		buf.WriteString(strings.Repeat(" ", fill))
	}

	buf.WriteString(right)
	buf.WriteString(Reset)
}
