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
func renderPaneStatus(buf *strings.Builder, cell *mux.LayoutCell, isActive bool, pd PaneData) {
	// Move to cell position
	buf.WriteString(CursorTo(cell.Y+1, cell.X+1))

	// Background: subtle dark surface
	buf.WriteString(Surface0Bg)

	color := pd.Color()

	// Status icon with pane color
	if isActive {
		buf.WriteString(hexToANSI(color))
		buf.WriteString("●")
	} else {
		buf.WriteString(DimFg)
		buf.WriteString("○")
	}

	// Name in bold with pane color
	buf.WriteString(" ")
	if isActive {
		buf.WriteString(Bold)
		buf.WriteString(hexToANSI(color))
	} else {
		buf.WriteString(TextFg)
	}
	buf.WriteString(fmt.Sprintf("[%s]", pd.Name()))
	buf.WriteString(NoBold)

	// Host (only if not mux.DefaultHost)
	if pd.Host() != "" && pd.Host() != mux.DefaultHost {
		buf.WriteString(GreenFg)
		buf.WriteString(fmt.Sprintf(" @%s", pd.Host()))
	}

	// Task
	if pd.Task() != "" {
		buf.WriteString(TextFg)
		buf.WriteString(fmt.Sprintf(" %s", pd.Task()))
	}

	// Fill remaining width with spaces
	// Calculate how many chars we've written (rough estimate)
	usedWidth := 2 + len(pd.Name()) + 2 // "● [name]"
	if pd.Host() != "" && pd.Host() != mux.DefaultHost {
		usedWidth += 2 + len(pd.Host())
	}
	if pd.Task() != "" {
		usedWidth += 1 + len(pd.Task())
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
