package render

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/weill-labs/amux/internal/mux"
)

// GlobalBarHeight is the number of rows reserved for the global status bar.
const GlobalBarHeight = 1

// renderPaneStatus draws a per-pane status line at the top of a pane cell.
// Format: ● [name] @host task
//
// Icon states:
//   - Active pane:          ● (filled, pane color)
//   - Inactive + busy:      ○ (hollow circle, dim)
//   - Inactive + idle:      ◇ (diamond outline, dim)
func renderPaneStatus(buf *strings.Builder, cell *mux.LayoutCell, isActive bool, pd PaneData) {
	writeCursorTo(buf, cell.Y+1, cell.X+1)

	// Background: subtle dark surface
	buf.WriteString(Surface0Bg)

	color := pd.Color()

	// Status icon: active=●, inactive+busy=○, inactive+idle=◇
	if isActive {
		buf.WriteString(hexToANSI(color))
		buf.WriteString("●")
	} else if pd.Idle() {
		buf.WriteString(DimFg)
		buf.WriteString("◇")
	} else {
		buf.WriteString(DimFg)
		buf.WriteString("○")
	}

	// Name: active=bold+color, inactive+busy=text, inactive+idle=dim
	buf.WriteString(" ")
	if isActive {
		buf.WriteString(Bold)
		buf.WriteString(hexToANSI(color))
	} else if pd.Idle() {
		buf.WriteString(DimFg)
	} else {
		buf.WriteString(TextFg)
	}
	buf.WriteString("[")
	buf.WriteString(pd.Name())
	buf.WriteString("]")
	buf.WriteString(NoBold)

	// Copy mode indicator + search prompt
	if pd.InCopyMode() {
		buf.WriteString(" ")
		buf.WriteString(YellowFg)
		if search := pd.CopyModeSearch(); search != "" {
			buf.WriteString("[copy] ")
			buf.WriteString(search)
		} else {
			buf.WriteString("[copy]")
		}
	}

	// Host (only if not mux.DefaultHost)
	if pd.Host() != "" && pd.Host() != mux.DefaultHost {
		buf.WriteString(GreenFg)
		buf.WriteString(" @")
		buf.WriteString(pd.Host())
	}

	// Task
	if pd.Task() != "" {
		buf.WriteString(TextFg)
		buf.WriteString(" ")
		buf.WriteString(pd.Task())
	}

	// Fill remaining width with spaces
	usedWidth := 2 + len(pd.Name()) + 2 // "● [name]"
	if pd.InCopyMode() {
		usedWidth += 7 // " [copy]"
		if search := pd.CopyModeSearch(); search != "" {
			usedWidth += 1 + len(search) // " /query"
		}
	}
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
func renderGlobalBar(buf *strings.Builder, sessionName string, paneCount int, width, yPos int, windows []WindowInfo) {
	writeCursorTo(buf, yPos+1, 1)

	// Catppuccin surface0 bg, text fg
	buf.WriteString(Surface0Bg + TextFg)

	now := time.Now().Format("15:04")

	left := " " + Bold + "amux" + NoBold + " │ "
	leftVisible := 8 // " amux │ "

	// Show window tabs if there are multiple windows
	if len(windows) > 1 {
		for _, w := range windows {
			tab := strconv.Itoa(w.Index) + ":" + w.Name
			if w.IsActive {
				left += Bold + "[" + tab + "]" + NoBold + " "
				leftVisible += len(tab) + 3 // "[tab] "
			} else {
				left += tab + " "
				leftVisible += len(tab) + 1
			}
		}
		left += "│ "
		leftVisible += 2
	} else {
		left += sessionName + " "
		leftVisible += len(sessionName) + 1
	}

	paneCountStr := strconv.Itoa(paneCount)
	right := " " + paneCountStr + " panes │ " + now + " "
	rightVisible := utf8.RuneCountInString(right)

	buf.WriteString(left)

	// Fill middle
	fill := width - leftVisible - rightVisible
	if fill > 0 {
		buf.WriteString(strings.Repeat(" ", fill))
	}

	buf.WriteString(right)
	buf.WriteString(Reset)
}
