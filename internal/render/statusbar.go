package render

import (
	"strconv"
	"strings"
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

	color := pd.Color()

	// Background: pane-tinted surface (active=25% blend, inactive=12%)
	buf.WriteString(hexToANSIBg(statusBarBgHex(color, isActive)))
	idle := !isActive && pd.Idle() // call once — may fork pgrep on server side

	// Status icon: active=●, inactive+busy=○, inactive+idle=◇
	if isActive {
		buf.WriteString(hexToANSI(color))
		buf.WriteString("●")
	} else if idle {
		buf.WriteString(DimFg)
		buf.WriteString("◇")
	} else {
		buf.WriteString(DimFg)
		buf.WriteString("○")
	}

	// Name: active=bold+text (accent is in the bg), inactive+busy=text, inactive+idle=dim
	buf.WriteString(" ")
	if isActive {
		buf.WriteString(Bold)
		buf.WriteString(TextFg)
	} else if idle {
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

	// Connection status indicator (remote panes only)
	if cs := pd.ConnStatus(); cs != "" {
		switch cs {
		case "connected":
			buf.WriteString(GreenFg)
			buf.WriteString(" ⚡")
		case "reconnecting":
			buf.WriteString(YellowFg)
			buf.WriteString(" ⟳")
		case "disconnected":
			buf.WriteString(RedFg)
			buf.WriteString(" ✕")
		}
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
	if cs := pd.ConnStatus(); cs != "" {
		usedWidth += 2 // " ⚡" or " ⟳" or " ✕" (space + 1 rune)
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

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max == 1 {
		return string(runes[:1])
	}
	return string(runes[:max-1]) + "…"
}

// renderGlobalBar draws the global status bar at the bottom of the terminal.
func renderGlobalBar(buf *strings.Builder, sessionName string, paneCount int, width, yPos int, windows []WindowInfo, message string) {
	writeCursorTo(buf, yPos+1, 1)

	// Catppuccin surface0 bg, text fg
	buf.WriteString(Surface0Bg + TextFg)

	now := timeNow().Format("15:04")

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

	right := ""
	rightColor := TextFg
	if message != "" {
		maxText := width - leftVisible - 2
		right = " " + truncateRunes(message, maxText) + " "
		rightColor = RedFg
		message = ""
	} else {
		paneCountStr := strconv.Itoa(paneCount)
		right = " " + paneCountStr + " panes │ " + now + " "
	}
	rightVisible := utf8.RuneCountInString(right)

	buf.WriteString(left)

	fill := width - leftVisible - rightVisible
	messageRunes := []rune(message)
	if fill > 0 {
		if len(messageRunes) > fill {
			messageRunes = messageRunes[:fill]
		}
		buf.WriteString(string(messageRunes))
		if remaining := fill - len(messageRunes); remaining > 0 {
			buf.WriteString(strings.Repeat(" ", remaining))
		}
	}

	buf.WriteString(rightColor)
	buf.WriteString(right)
	buf.WriteString(Reset)
}
