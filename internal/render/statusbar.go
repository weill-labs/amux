package render

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

// GlobalBarHeight is the number of rows reserved for the global status bar.
const GlobalBarHeight = 1

const globalBarPrefixVisibleWidth = 8 // " amux │ "
const missingIssueHint = "set issue"

type globalBarWindowTab struct {
	window  WindowInfo
	display string
	start   int
	end     int
}

type paneStatusMetadataItem struct {
	text   string
	status proto.TrackedStatus
}

type paneStatusMetadataSegment struct {
	text   string
	status proto.TrackedStatus
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

	metaItems := paneStatusMetadataItems(pd.TrackedPRs(), pd.TrackedIssues(), isActive)
	metaText := renderPaneStatusMetadataANSI(metaItems, availableMetadataWidth(cell.W, pd, isActive))

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

func paneStatusMetadataItems(prs []proto.TrackedPR, issues []proto.TrackedIssue, showMissingIssueHint bool) []paneStatusMetadataItem {
	items := make([]paneStatusMetadataItem, 0, len(prs)+len(issues)+1)
	for _, pr := range prs {
		if pr.Number <= 0 {
			continue
		}
		items = append(items, paneStatusMetadataItem{
			text:   "#" + strconv.Itoa(pr.Number),
			status: normalizeTrackedStatus(pr.Status),
		})
	}
	hasIssue := false
	for _, issue := range issues {
		id := strings.TrimSpace(issue.ID)
		if id == "" {
			continue
		}
		hasIssue = true
		items = append(items, paneStatusMetadataItem{
			text:   id,
			status: normalizeTrackedStatus(issue.Status),
		})
	}
	if showMissingIssueHint && !hasIssue {
		items = append(items, paneStatusMetadataItem{text: missingIssueHint})
	}
	return items
}

func availableMetadataWidth(cellWidth int, pd PaneData, showMissingIssueHint bool) int {
	if len(paneStatusMetadataItems(pd.TrackedPRs(), pd.TrackedIssues(), showMissingIssueHint)) == 0 {
		return 0
	}
	return cellWidth - paneStatusUsedWidthWithoutMetadata(pd) - 1
}

func normalizeTrackedStatus(status proto.TrackedStatus) proto.TrackedStatus {
	if status == "" {
		return proto.TrackedStatusUnknown
	}
	return status
}

func paneStatusMetadataSegments(items []paneStatusMetadataItem, maxWidth int) []paneStatusMetadataSegment {
	if len(items) == 0 || maxWidth < 2 {
		return nil
	}

	segments := make([]paneStatusMetadataSegment, 0, len(items)*2)
	usedWidth := 0
	for i, item := range items {
		labelWidth := runewidth.StringWidth(item.text)
		if labelWidth <= 0 {
			continue
		}

		if i == 0 {
			if labelWidth <= maxWidth {
				segments = append(segments, paneStatusMetadataSegment{text: item.text, status: item.status})
				usedWidth = labelWidth
				continue
			}

			prefix := truncateRunewidth(item.text, maxWidth-1)
			if prefix == "" {
				return nil
			}
			return []paneStatusMetadataSegment{
				{text: prefix, status: item.status},
				{text: "…"},
			}
		}

		if usedWidth+2+labelWidth <= maxWidth {
			segments = append(segments,
				paneStatusMetadataSegment{text: ", "},
				paneStatusMetadataSegment{text: item.text, status: item.status},
			)
			usedWidth += 2 + labelWidth
			continue
		}

		if usedWidth < maxWidth {
			segments = append(segments, paneStatusMetadataSegment{text: "…"})
		}
		return segments
	}

	return segments
}

func renderPaneStatusMetadataANSI(items []paneStatusMetadataItem, maxWidth int) string {
	segments := paneStatusMetadataSegments(items, maxWidth)
	if len(segments) == 0 {
		return ""
	}

	buf := strings.Builder{}
	for _, segment := range segments {
		if segment.text == "" {
			continue
		}
		if segment.status == proto.TrackedStatusCompleted {
			buf.WriteString(DimFg)
			buf.WriteString(StrikeOn)
			buf.WriteString(segment.text)
			buf.WriteString(StrikeOff)
			buf.WriteString(TextFg)
			continue
		}
		buf.WriteString(segment.text)
	}
	return buf.String()
}

func truncateRunewidth(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}

	buf := strings.Builder{}
	usedWidth := 0
	for _, r := range s {
		runeWidth := runewidth.RuneWidth(r)
		if runeWidth <= 0 {
			runeWidth = 1
		}
		if usedWidth+runeWidth > maxWidth {
			break
		}
		buf.WriteRune(r)
		usedWidth += runeWidth
	}
	return buf.String()
}

func paneStatusUsedWidthWithoutMetadata(pd PaneData) int {
	usedWidth := 2 + runewidth.StringWidth(pd.Name()) + 2 // "● [name]"
	if pd.IsLead() {
		usedWidth += 7 // " [lead]"
	}
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
