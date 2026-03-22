package render

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/weill-labs/amux/internal/mux"
)

// GlobalBarHeight is the number of rows reserved for the global status bar.
const GlobalBarHeight = 1

const globalBarPrefixVisibleWidth = 8 // " amux │ "

type globalBarWindowTab struct {
	window  WindowInfo
	display string
	start   int
	end     int
}

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

	// Name: active=bold+color, inactive+busy=text, inactive+idle=dim
	buf.WriteString(" ")
	if isActive {
		buf.WriteString(Bold)
		buf.WriteString(hexToANSI(color))
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

func buildGlobalBarWindowTabs(windows []WindowInfo) []globalBarWindowTab {
	if len(windows) <= 1 {
		return nil
	}

	tabs := make([]globalBarWindowTab, 0, len(windows))
	col := globalBarPrefixVisibleWidth
	for _, w := range windows {
		label := strconv.Itoa(w.Index) + ":" + w.Name
		display := label
		if w.IsActive {
			display = "[" + label + "]"
		}

		width := utf8.RuneCountInString(display)
		tabs = append(tabs, globalBarWindowTab{
			window:  w,
			display: display,
			start:   col,
			end:     col + width,
		})
		col += width + 1
	}
	return tabs
}

// GlobalBarWindowAtColumn resolves a 0-based terminal column within the
// rendered global bar to the corresponding window tab.
func GlobalBarWindowAtColumn(windows []WindowInfo, x int) (WindowInfo, bool) {
	for _, tab := range buildGlobalBarWindowTabs(windows) {
		if x >= tab.start && x < tab.end {
			return tab.window, true
		}
	}
	return WindowInfo{}, false
}

// renderGlobalBar draws the global status bar at the bottom of the terminal.
func renderGlobalBar(buf *strings.Builder, sessionName string, paneCount int, width, yPos int, windows []WindowInfo, message string) {
	writeCursorTo(buf, yPos+1, 1)

	// Catppuccin surface0 bg, text fg
	buf.WriteString(Surface0Bg + TextFg)

	now := timeNow().Format("15:04")
	tabs := buildGlobalBarWindowTabs(windows)

	left := " " + Bold + "amux" + NoBold + " │ "
	leftVisible := globalBarPrefixVisibleWidth

	// Show window tabs if there are multiple windows
	if len(tabs) > 0 {
		for _, tab := range tabs {
			if tab.window.IsActive {
				left += Bold + tab.display + NoBold + " "
			} else {
				left += tab.display + " "
			}
			leftVisible += utf8.RuneCountInString(tab.display) + 1
		}
		left += "│ "
		leftVisible += 2
	} else {
		left += sessionName + " "
		leftVisible += utf8.RuneCountInString(sessionName) + 1
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
