package render

import (
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

// GlobalBarHeight is the number of rows reserved for the global status bar.
const GlobalBarHeight = 1

const (
	globalBarTitlePrefixVisibleWidth = 8  // " amux │ "
	globalBarHelpVisibleWidth        = 6  // "? help"
)
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

type paneStatusSegmentRole int

const (
	paneStatusSegmentBackground paneStatusSegmentRole = iota
	paneStatusSegmentPane
	paneStatusSegmentPaneBold
	paneStatusSegmentDim
	paneStatusSegmentText
	paneStatusSegmentYellow
	paneStatusSegmentGreen
	paneStatusSegmentRed
	paneStatusSegmentCompletedMeta
)

type paneStatusSegment struct {
	text string
	role paneStatusSegmentRole
}

func paneStatusColorHex(pd PaneData) string {
	if color := pd.Color(); color != "" {
		return color
	}
	return config.TextColorHex
}

func appendPaneStatusSegment(segments []paneStatusSegment, text string, role paneStatusSegmentRole) []paneStatusSegment {
	if text == "" {
		return segments
	}
	return append(segments, paneStatusSegment{text: text, role: role})
}

func buildPaneStatusSegments(cellWidth int, isActive bool, pd PaneData) []paneStatusSegment {
	idle := !isActive && pd.Idle()

	segments := make([]paneStatusSegment, 0, 16)

	switch {
	case pd.IsLead() && isActive:
		segments = appendPaneStatusSegment(segments, "▶", paneStatusSegmentPane)
	case pd.IsLead():
		segments = appendPaneStatusSegment(segments, "▶", paneStatusSegmentDim)
	case isActive:
		segments = appendPaneStatusSegment(segments, "●", paneStatusSegmentPane)
	case idle:
		segments = appendPaneStatusSegment(segments, "◇", paneStatusSegmentDim)
	default:
		segments = appendPaneStatusSegment(segments, "○", paneStatusSegmentDim)
	}

	segments = appendPaneStatusSegment(segments, " ", paneStatusSegmentBackground)

	switch {
	case isActive:
		segments = appendPaneStatusSegment(segments, "["+pd.Name()+"]", paneStatusSegmentPaneBold)
	case idle:
		segments = appendPaneStatusSegment(segments, "["+pd.Name()+"]", paneStatusSegmentDim)
	default:
		segments = appendPaneStatusSegment(segments, "["+pd.Name()+"]", paneStatusSegmentText)
	}

	if pd.IsLead() {
		segments = appendPaneStatusSegment(segments, " ", paneStatusSegmentBackground)
		segments = appendPaneStatusSegment(segments, "[lead]", paneStatusSegmentPane)
	}

	if pd.InCopyMode() {
		segments = appendPaneStatusSegment(segments, " ", paneStatusSegmentBackground)
		copyText := "[copy]"
		if search := pd.CopyModeSearch(); search != "" {
			copyText += " " + search
		}
		segments = appendPaneStatusSegment(segments, copyText, paneStatusSegmentYellow)
	}

	metaItems := paneStatusMetadataItems(pd.TrackedPRs(), pd.TrackedIssues(), isActive)
	metaSegments := paneStatusMetadataSegments(metaItems, availableMetadataWidth(cellWidth, pd, isActive))
	if len(metaSegments) > 0 {
		segments = appendPaneStatusSegment(segments, " ", paneStatusSegmentBackground)
		for _, segment := range metaSegments {
			role := paneStatusSegmentText
			if segment.status == proto.TrackedStatusCompleted {
				role = paneStatusSegmentCompletedMeta
			}
			segments = appendPaneStatusSegment(segments, segment.text, role)
		}
	}

	if pd.Host() != "" && pd.Host() != mux.DefaultHost {
		segments = appendPaneStatusSegment(segments, " ", paneStatusSegmentBackground)
		segments = appendPaneStatusSegment(segments, "@"+pd.Host(), paneStatusSegmentGreen)
	}

	if cs := pd.ConnStatus(); cs != "" {
		segments = appendPaneStatusSegment(segments, " ", paneStatusSegmentBackground)
		switch cs {
		case "connected":
			segments = appendPaneStatusSegment(segments, "⚡", paneStatusSegmentGreen)
		case "reconnecting":
			segments = appendPaneStatusSegment(segments, "⟳", paneStatusSegmentYellow)
		case "disconnected":
			segments = appendPaneStatusSegment(segments, "✕", paneStatusSegmentRed)
		}
	}

	if pd.Task() != "" {
		segments = appendPaneStatusSegment(segments, " ", paneStatusSegmentBackground)
		segments = appendPaneStatusSegment(segments, pd.Task(), paneStatusSegmentText)
	}

	return segments
}

func paneStatusSegmentsWidth(segments []paneStatusSegment) int {
	usedWidth := 0
	for _, segment := range segments {
		usedWidth += runewidth.StringWidth(segment.text)
	}
	return usedWidth
}

// renderPaneStatus draws a per-pane status line at the top of a pane cell.
// Format: ● [name] @host task
//
// Icon states:
//   - Active pane:          ● (filled, pane color)
//   - Inactive + busy:      ○ (hollow circle, dim)
//   - Inactive + idle:      ◇ (diamond outline, dim)
func renderPaneStatus(buf *strings.Builder, cell *mux.LayoutCell, isActive bool, pd PaneData) {
	renderPaneStatusWithProfile(buf, cell, isActive, pd, defaultColorProfile)
}

func renderPaneStatusWithProfile(buf *strings.Builder, cell *mux.LayoutCell, isActive bool, pd PaneData, profile termenv.Profile) {
	writeCursorTo(buf, cell.Y+1, cell.X+1)

	styles := newStatusBarStyles(paneStatusColorHex(pd))
	segments := buildPaneStatusSegments(cell.W, isActive, pd)
	for _, segment := range segments {
		writeStyledTextWithProfile(buf, styles.pane(segment.role), segment.text, profile)
	}

	remaining := cell.W - paneStatusSegmentsWidth(segments)
	if remaining > 0 {
		writeStyledTextWithProfile(buf, styles.background, strings.Repeat(" ", remaining), profile)
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

	orderedItems := orderPaneStatusMetadataItems(items)
	segments := make([]paneStatusMetadataSegment, 0, len(orderedItems)*2)
	usedWidth := 0
	for i, item := range orderedItems {
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

func orderPaneStatusMetadataItems(items []paneStatusMetadataItem) []paneStatusMetadataItem {
	ordered := append([]paneStatusMetadataItem(nil), items...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return paneStatusMetadataIsCompleted(ordered[j].status) && !paneStatusMetadataIsCompleted(ordered[i].status)
	})
	return ordered
}

func paneStatusMetadataIsCompleted(status proto.TrackedStatus) bool {
	return normalizeTrackedStatus(status) == proto.TrackedStatusCompleted
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
	col := globalBarTitlePrefixVisibleWidth
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

func globalBarLeftVisibleWidth(sessionName string, windows []WindowInfo) int {
	leftVisible := globalBarTitlePrefixVisibleWidth
	if len(windows) > 1 {
		for _, tab := range buildGlobalBarWindowTabs(windows) {
			leftVisible += utf8.RuneCountInString(tab.display) + 1
		}
		return leftVisible + 2
	}
	return leftVisible + utf8.RuneCountInString(sessionName) + 1
}

func globalBarStatusPrefix(paneCount int) string {
	return " " + strconv.Itoa(paneCount) + " panes │ "
}

func globalBarStatusRightText(paneCount int, showHelp bool, now time.Time) string {
	right := globalBarStatusPrefix(paneCount)
	if showHelp {
		right += "? help │ "
	}
	return right + now.Format("15:04") + " "
}

func globalBarStatusRightWidth(paneCount int, showHelp bool, now time.Time) int {
	return utf8.RuneCountInString(globalBarStatusRightText(paneCount, showHelp, now))
}

func globalBarHelpColumns(width, paneCount int, showHelp bool, now time.Time) (start, end int, ok bool) {
	if !showHelp {
		return 0, 0, false
	}
	rightVisible := globalBarStatusRightWidth(paneCount, showHelp, now)
	rightStart := width - rightVisible
	start = rightStart + utf8.RuneCountInString(globalBarStatusPrefix(paneCount))
	return start, start + globalBarHelpVisibleWidth, true
}

func globalBarShowsHelp(width int, sessionName string, paneCount int, windows []WindowInfo, message string, now time.Time) bool {
	if message != "" {
		return false
	}
	return width >= globalBarLeftVisibleWidth(sessionName, windows)+globalBarStatusRightWidth(paneCount, true, now)
}

// GlobalBarShowsHelp reports whether the current terminal width can render the
// clickable "? help" segment without colliding with the right-side status text.
func GlobalBarShowsHelp(width int, sessionName string, paneCount int, windows []WindowInfo, message string, now time.Time) bool {
	return globalBarShowsHelp(width, sessionName, paneCount, windows, message, now)
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

// GlobalBarHelpToggleAtColumn reports whether x hits the clickable "? help"
// region in the global status bar.
func GlobalBarHelpToggleAtColumn(x, width, paneCount int, showHelp bool, now time.Time) bool {
	start, end, ok := globalBarHelpColumns(width, paneCount, showHelp, now)
	if !ok {
		return false
	}
	return x >= start && x < end
}

// renderGlobalBar draws the global status bar at the bottom of the terminal.
func renderGlobalBar(buf *strings.Builder, sessionName string, paneCount int, width, yPos int, windows []WindowInfo, message string, now time.Time) {
	renderGlobalBarWithProfile(buf, sessionName, paneCount, width, yPos, windows, message, now, defaultColorProfile)
}

func renderGlobalBarWithProfile(buf *strings.Builder, sessionName string, paneCount int, width, yPos int, windows []WindowInfo, message string, now time.Time, profile termenv.Profile) {
	writeCursorTo(buf, yPos+1, 1)
	styles := newStatusBarStyles(config.TextColorHex)

	showHelp := globalBarShowsHelp(width, sessionName, paneCount, windows, message, now)
	tabs := buildGlobalBarWindowTabs(windows)
	leftVisible := globalBarLeftVisibleWidth(sessionName, windows)
	writeStyledTextWithProfile(buf, styles.background, " ", profile)
	writeStyledTextWithProfile(buf, styles.title, "amux", profile)
	writeStyledTextWithProfile(buf, styles.busy, " │ ", profile)

	// Show window tabs if there are multiple windows
	if len(tabs) > 0 {
		for _, tab := range tabs {
			writeStyledTextWithProfile(buf, styles.windowTab(tab.window), tab.display, profile)
			writeStyledTextWithProfile(buf, styles.busy, " ", profile)
		}
		writeStyledTextWithProfile(buf, styles.busy, "│ ", profile)
	} else {
		writeStyledTextWithProfile(buf, styles.busy, sessionName+" ", profile)
	}

	right := ""
	rightStyle := styles.busy
	if message != "" {
		maxText := width - leftVisible - 2
		right = " " + truncateRunes(message, maxText) + " "
		rightStyle = styles.error
	} else {
		right = globalBarStatusRightText(paneCount, showHelp, now)
	}
	rightVisible := utf8.RuneCountInString(right)

	fill := width - leftVisible - rightVisible
	if fill > 0 {
		writeStyledTextWithProfile(buf, styles.background, strings.Repeat(" ", fill), profile)
	}

	writeStyledTextWithProfile(buf, rightStyle, right, profile)
	buf.WriteString(Reset)
}
