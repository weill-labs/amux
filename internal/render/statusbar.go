package render

import (
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
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
	globalBarTitlePrefixVisibleWidth = 8 // " amux │ "
	globalBarHelpVisibleWidth        = 6 // "? help"
)

type globalBarWindowTab struct {
	window  WindowInfo
	display string
	start   int
	end     int
}

// GlobalBarWindowDropTarget describes a tab reorder destination resolved from
// a hovered global-bar tab.
type GlobalBarWindowDropTarget struct {
	DestinationIndex int
	IndicatorColumn  int
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

func buildPaneStatusSegmentsWithIcons(cellWidth int, isActive bool, pd PaneData, icons IconSet) []paneStatusSegment {
	icons = normalizeIconSet(icons)
	idle := !isActive && pd.Idle()

	segments := make([]paneStatusSegment, 0, 16)

	switch {
	case pd.IsLead() && isActive:
		segments = appendPaneStatusSegment(segments, icons.PaneLead, paneStatusSegmentPane)
	case pd.IsLead():
		segments = appendPaneStatusSegment(segments, icons.PaneLead, paneStatusSegmentDim)
	case isActive:
		segments = appendPaneStatusSegment(segments, icons.PaneActive, paneStatusSegmentPane)
	case idle:
		segments = appendPaneStatusSegment(segments, icons.PaneIdle, paneStatusSegmentDim)
	default:
		segments = appendPaneStatusSegment(segments, icons.PaneBusy, paneStatusSegmentDim)
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
		copyText := icons.CopyMode
		if search := pd.CopyModeSearch(); search != "" {
			copyText += " " + search
		}
		segments = appendPaneStatusSegment(segments, copyText, paneStatusSegmentYellow)
	}

	metaItems := paneStatusMetadataItemsForPaneWithIcons(pd, icons)
	metaSegments := paneStatusMetadataSegments(metaItems, availableMetadataWidthWithIcons(cellWidth, isActive, pd, icons))
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
		segments = appendPaneStatusSegment(segments, icons.RemoteHost+pd.Host(), paneStatusSegmentGreen)
	}

	if cs := pd.ConnStatus(); cs != "" {
		segments = appendPaneStatusSegment(segments, " ", paneStatusSegmentBackground)
		switch cs {
		case string(proto.Connected):
			segments = appendPaneStatusSegment(segments, icons.Connected, paneStatusSegmentGreen)
		case string(proto.Reconnecting):
			segments = appendPaneStatusSegment(segments, icons.Reconnecting, paneStatusSegmentYellow)
		case string(proto.Disconnected):
			segments = appendPaneStatusSegment(segments, icons.Disconnected, paneStatusSegmentRed)
		}
	}

	if taskText := paneStatusTaskText(pd.Task(), icons); taskText != "" {
		segments = appendPaneStatusSegment(segments, " ", paneStatusSegmentBackground)
		segments = appendPaneStatusSegment(segments, taskText, paneStatusSegmentText)
	}

	return clipPaneStatusSegments(segments, cellWidth)
}

func paneStatusSegmentsWidth(segments []paneStatusSegment) int {
	usedWidth := 0
	for _, segment := range segments {
		usedWidth += runewidth.StringWidth(segment.text)
	}
	return usedWidth
}

func clipPaneStatusSegments(segments []paneStatusSegment, maxWidth int) []paneStatusSegment {
	if maxWidth <= 0 || len(segments) == 0 {
		return nil
	}
	if paneStatusSegmentsWidth(segments) <= maxWidth {
		return segments
	}
	if maxWidth == 1 {
		return []paneStatusSegment{{text: "…", role: paneStatusSegmentText}}
	}

	clipped := make([]paneStatusSegment, 0, len(segments)+1)
	remaining := maxWidth - 1
	ellipsisRole := paneStatusSegmentText

	for _, segment := range segments {
		segWidth := runewidth.StringWidth(segment.text)
		if segWidth <= 0 {
			continue
		}
		if segWidth <= remaining {
			clipped = append(clipped, segment)
			remaining -= segWidth
			ellipsisRole = segment.role
			continue
		}

		prefix := truncateRunewidth(segment.text, remaining)
		if prefix != "" {
			clipped = append(clipped, paneStatusSegment{text: prefix, role: segment.role})
			ellipsisRole = segment.role
		}
		break
	}

	clipped = trimPaneStatusSegmentsRight(clipped)
	if len(clipped) > 0 {
		ellipsisRole = clipped[len(clipped)-1].role
	}
	return append(clipped, paneStatusSegment{text: "…", role: ellipsisRole})
}

func trimPaneStatusSegmentsRight(segments []paneStatusSegment) []paneStatusSegment {
	for len(segments) > 0 {
		last := segments[len(segments)-1]
		trimmed := strings.TrimRightFunc(last.text, func(r rune) bool {
			return unicode.IsSpace(r) || r == ','
		})
		if trimmed == last.text {
			return segments
		}
		if trimmed == "" {
			segments = segments[:len(segments)-1]
			continue
		}
		segments[len(segments)-1].text = trimmed
		return segments
	}
	return segments
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
	renderPaneStatusPressedWithProfile(buf, cell, isActive, false, pd, profile)
}

func renderPaneStatusPressedWithProfile(buf *strings.Builder, cell *mux.LayoutCell, isActive, pressed bool, pd PaneData, profile termenv.Profile) {
	renderPaneStatusPressedWithProfileAndIcons(buf, cell, isActive, pressed, pd, profile, DefaultIconSet())
}

func renderPaneStatusPressedWithProfileAndIcons(buf *strings.Builder, cell *mux.LayoutCell, isActive, pressed bool, pd PaneData, profile termenv.Profile, icons IconSet) {
	writeCursorTo(buf, cell.Y+1, cell.X+1)

	styles := newStatusBarStylesPressed(paneStatusColorHex(pd), pressed)
	segments := buildPaneStatusSegmentsWithIcons(cell.W, isActive, pd, icons)
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

func paneStatusMetadataItemsWithIcons(prs []proto.TrackedPR, issues []proto.TrackedIssue, rawIssue string, icons IconSet) []paneStatusMetadataItem {
	icons = normalizeIconSet(icons)
	issues = paneStatusTrackedIssues(issues, rawIssue)
	items := make([]paneStatusMetadataItem, 0, len(prs)+len(issues))
	for _, pr := range prs {
		if pr.Number <= 0 {
			continue
		}
		items = append(items, paneStatusMetadataItem{
			text:   icons.PR + strconv.Itoa(pr.Number),
			status: normalizeTrackedStatus(pr.Status),
		})
	}
	for _, issue := range issues {
		id := strings.TrimSpace(issue.ID)
		if id == "" {
			continue
		}
		items = append(items, paneStatusMetadataItem{
			text:   icons.Issue + id,
			status: normalizeTrackedStatus(issue.Status),
		})
	}
	return items
}

func paneStatusTrackedIssues(issues []proto.TrackedIssue, rawIssue string) []proto.TrackedIssue {
	if len(issues) > 0 {
		return issues
	}

	id := strings.TrimSpace(rawIssue)
	if id == "" {
		return nil
	}
	return []proto.TrackedIssue{{
		ID:     id,
		Status: proto.TrackedStatusActive,
	}}
}

func paneStatusMetadataItemsForPaneWithIcons(pd PaneData, icons IconSet) []paneStatusMetadataItem {
	return paneStatusMetadataItemsWithIcons(pd.TrackedPRs(), pd.TrackedIssues(), pd.Issue(), icons)
}

func availableMetadataWidth(cellWidth int, pd PaneData) int {
	return availableMetadataWidthWithIcons(cellWidth, true, pd, DefaultIconSet())
}

func availableMetadataWidthWithIcons(cellWidth int, isActive bool, pd PaneData, icons IconSet) int {
	if len(paneStatusMetadataItemsForPaneWithIcons(pd, icons)) == 0 {
		return 0
	}
	return cellWidth - paneStatusUsedWidthWithoutMetadataWithIcons(isActive, pd, icons) - 1
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
				segments = append(segments, paneStatusMetadataSegment(item))
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
				paneStatusMetadataSegment(item),
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

func paneStatusTaskText(task string, icons IconSet) string {
	if task == "" {
		return ""
	}
	icons = normalizeIconSet(icons)
	if icons.Task == "" {
		return task
	}
	return icons.Task + " " + task
}

func paneStatusUsedWidthWithoutMetadataWithIcons(isActive bool, pd PaneData, icons IconSet) int {
	icons = normalizeIconSet(icons)
	usedWidth := runewidth.StringWidth(paneStatusStateIcon(isActive, pd, icons)) + 1 + runewidth.StringWidth(pd.Name()) + 2 // "● [name]"
	if pd.IsLead() {
		usedWidth += 7 // " [lead]"
	}
	if pd.InCopyMode() {
		usedWidth += 1 + runewidth.StringWidth(icons.CopyMode)
		if search := pd.CopyModeSearch(); search != "" {
			usedWidth += 1 + runewidth.StringWidth(search)
		}
	}
	if pd.Host() != "" && pd.Host() != mux.DefaultHost {
		usedWidth += 1 + runewidth.StringWidth(icons.RemoteHost) + runewidth.StringWidth(pd.Host())
	}
	if cs := pd.ConnStatus(); cs != "" {
		usedWidth += 1 + runewidth.StringWidth(connStatusIcon(cs, icons))
	}
	if taskText := paneStatusTaskText(pd.Task(), icons); taskText != "" {
		usedWidth += 1 + runewidth.StringWidth(taskText)
	}
	return usedWidth
}

func paneStatusStateIcon(isActive bool, pd PaneData, icons IconSet) string {
	icons = normalizeIconSet(icons)
	switch {
	case pd.IsLead():
		return icons.PaneLead
	case isActive:
		return icons.PaneActive
	case pd.Idle():
		return icons.PaneIdle
	default:
		return icons.PaneBusy
	}
}

func connStatusIcon(status string, icons IconSet) string {
	icons = normalizeIconSet(icons)
	switch status {
	case string(proto.Connected):
		return icons.Connected
	case string(proto.Reconnecting):
		return icons.Reconnecting
	case string(proto.Disconnected):
		return icons.Disconnected
	default:
		return ""
	}
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

// GlobalBarWindowDropTargetAtColumn resolves a hovered tab to a destination
// index and insertion-marker column for drag-reordering window tabs.
func GlobalBarWindowDropTargetAtColumn(windows []WindowInfo, sourceIndex, x int) (GlobalBarWindowDropTarget, bool) {
	tabs := buildGlobalBarWindowTabs(windows)
	if len(tabs) == 0 || sourceIndex < 1 || sourceIndex > len(tabs) {
		return GlobalBarWindowDropTarget{}, false
	}

	for i, tab := range tabs {
		if x < tab.start || x >= tab.end {
			continue
		}
		hoveredIndex := i + 1
		if hoveredIndex == sourceIndex {
			return GlobalBarWindowDropTarget{}, false
		}

		dest := hoveredIndex
		col := tab.start
		if x >= tab.start+(tab.end-tab.start)/2 {
			dest++
			col = tab.end
		} else if hoveredIndex > 1 {
			col = tabs[i-1].end
		}
		if sourceIndex < hoveredIndex {
			dest--
		}
		return GlobalBarWindowDropTarget{
			DestinationIndex: dest,
			IndicatorColumn:  col,
		}, true
	}

	return GlobalBarWindowDropTarget{}, false
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
