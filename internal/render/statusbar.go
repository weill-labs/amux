package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

// PaneStatusHeight is the number of rows reserved for the per-pane status line.
const PaneStatusHeight = 1

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
	buf.WriteString(fmt.Sprintf("\033[%d;%dH", cell.Y+1, cell.X+1))

	// Background: subtle dark surface
	buf.WriteString("\033[48;2;49;50;68m") // Catppuccin surface0 (#313244)

	// Status icon with pane color
	if isActive {
		buf.WriteString(hexToANSI(meta.Color))
		buf.WriteString("●")
	} else {
		buf.WriteString("\033[38;5;240m") // dim gray
		buf.WriteString("○")
	}

	// Name in bold with pane color
	buf.WriteString(" ")
	if isActive {
		buf.WriteString("\033[1m") // bold
		buf.WriteString(hexToANSI(meta.Color))
	} else {
		buf.WriteString("\033[38;2;205;214;244m") // Catppuccin text
	}
	buf.WriteString(fmt.Sprintf("[%s]", meta.Name))
	buf.WriteString("\033[22m") // unbold

	// Host (only if not "local")
	if meta.Host != "" && meta.Host != "local" {
		buf.WriteString("\033[38;2;166;227;161m") // Catppuccin green
		buf.WriteString(fmt.Sprintf(" @%s", meta.Host))
	}

	// Task
	if meta.Task != "" {
		buf.WriteString("\033[38;2;205;214;244m") // Catppuccin text
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

	buf.WriteString("\033[0m") // reset
}

// renderGlobalBar draws the global status bar at the bottom of the terminal.
func renderGlobalBar(buf *strings.Builder, sessionName string, paneCount int, width, yPos int) {
	buf.WriteString(fmt.Sprintf("\033[%d;1H", yPos+1))

	// Catppuccin surface0 bg, text fg
	buf.WriteString("\033[48;2;49;50;68m\033[38;2;205;214;244m")

	now := time.Now().Format("15:04")

	left := fmt.Sprintf(" \033[1mamux\033[22m │ %s ", sessionName)
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
	buf.WriteString("\033[0m")
}
