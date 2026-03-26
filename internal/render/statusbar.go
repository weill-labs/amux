package render

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"github.com/weill-labs/amux/internal/config"
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

	// Status icon: lead=▶, active=●, inactive+busy=○, inactive+idle=◇
	lead := pd.IsLead()
	if lead {
		if isActive {
			buf.WriteString(hexToANSI(color))
		} else {
			buf.WriteString(DimFg)
		}
		buf.WriteString("▶")
	} else if isActive {
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

	metaText := paneStatusMetadata(pd, availableMetadataWidth(cell.W, pd))

	// Lead indicator
	if lead {
		buf.WriteString(" ")
		buf.WriteString(hexToANSI(color))
		buf.WriteString("[lead]")
	}

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

	if metaText != "" {
		buf.WriteString(TextFg)
		buf.WriteString(" ")
		buf.WriteString(metaText)
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
	usedWidth := paneStatusUsedWidthWithoutMetadata(pd)
	if metaText != "" {
		usedWidth += 1 + runewidth.StringWidth(metaText)
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

func paneStatusMetadata(pd PaneData, maxWidth int) string {
	items := paneStatusMetadataItems(pd.PRs(), pd.Issues())
	if len(items) == 0 || maxWidth < 2 {
		return ""
	}

	text := strings.Join(items, ", ")
	if runewidth.StringWidth(text) <= maxWidth {
		return text
	}

	var buf strings.Builder
	usedWidth := 0
	for _, r := range text {
		runeWidth := runewidth.RuneWidth(r)
		if runeWidth <= 0 {
			runeWidth = 1
		}
		if usedWidth+runeWidth > maxWidth-1 {
			break
		}
		buf.WriteRune(r)
		usedWidth += runeWidth
	}

	prefix := strings.TrimRight(buf.String(), ", ")
	if prefix == "" {
		return ""
	}
	return prefix + "…"
}

func paneStatusMetadataItems(prs, issues []string) []string {
	items := make([]string, 0, len(prs)+len(issues))
	for _, pr := range prs {
		pr = strings.TrimSpace(pr)
		if pr == "" {
			continue
		}
		if !strings.HasPrefix(pr, "#") {
			pr = "#" + pr
		}
		items = append(items, pr)
	}
	for _, issue := range issues {
		issue = strings.TrimSpace(issue)
		if issue == "" {
			continue
		}
		items = append(items, issue)
	}
	return items
}

func availableMetadataWidth(cellWidth int, pd PaneData) int {
	if len(paneStatusMetadataItems(pd.PRs(), pd.Issues())) == 0 {
		return 0
	}
	return cellWidth - paneStatusUsedWidthWithoutMetadata(pd) - 1
}

func paneStatusUsedWidthWithoutMetadata(pd PaneData) int {
	usedWidth := 2 + runewidth.StringWidth(pd.Name()) + 2 // "● [name]"
	if pd.InCopyMode() {
		usedWidth += 7 // " [copy]"
		if search := pd.CopyModeSearch(); search != "" {
			usedWidth += 1 + runewidth.StringWidth(search)
		}
	}
	if pd.Host() != "" && pd.Host() != mux.DefaultHost {
		usedWidth += 2 + runewidth.StringWidth(pd.Host())
	}
	if cs := pd.ConnStatus(); cs != "" {
		usedWidth += 2 // " ⚡" or " ⟳" or " ✕"
	}
	if pd.Task() != "" {
		usedWidth += 1 + runewidth.StringWidth(pd.Task())
	}
	return usedWidth
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

func globalBarTabColorHex(window WindowInfo) string {
	if window.IsActive {
		return config.BlueHex
	}
	return config.TextColorHex
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
func renderGlobalBar(buf *strings.Builder, sessionName string, paneCount int, width, yPos int, windows []WindowInfo, message string, now time.Time) {
	writeCursorTo(buf, yPos+1, 1)

	// Catppuccin surface0 bg, text fg
	buf.WriteString(Surface0Bg + TextFg)

	nowStr := now.Format("15:04")
	tabs := buildGlobalBarWindowTabs(windows)

	left := " " + Bold + "amux" + NoBold + " │ "
	leftVisible := globalBarPrefixVisibleWidth

	// Show window tabs if there are multiple windows
	if len(tabs) > 0 {
		for _, tab := range tabs {
			left += hexToANSI(globalBarTabColorHex(tab.window))
			if tab.window.IsActive {
				left += Bold
			}
			left += tab.display
			if tab.window.IsActive {
				left += NoBold
			}
			left += TextFg + " "
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
		right = " " + paneCountStr + " panes │ " + nowStr + " "
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
