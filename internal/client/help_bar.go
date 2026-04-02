package client

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/mattn/go-runewidth"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/render"
)

type helpBindingSelector struct {
	action string
	args   []string
}

type helpBarItem struct {
	keys string
	desc string
}

type helpBarState struct {
	items []helpBarItem
}

func (st *helpBarState) view(width int) string {
	return strings.Join(st.rows(width), "\n")
}

func (st *helpBarState) renderOverlay(width int) *render.HelpBarOverlay {
	if st == nil {
		return nil
	}
	rows := st.rows(width)
	if len(rows) == 0 {
		return nil
	}
	return &render.HelpBarOverlay{Rows: rows}
}

func (st *helpBarState) rows(width int) []string {
	if st == nil {
		return nil
	}
	return layoutHelpBarRows(st.items, width)
}

func buildHelpBar(kb *config.Keybindings) *helpBarState {
	if kb == nil {
		return nil
	}

	items := compactHelpBarItems(
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "help"}), "close"),
		helpBarItemForKeys(helpBarJoinParts(
			helpBindingDisplay(kb, helpBindingSelector{action: "focus", args: []string{"left"}}),
			helpBindingDisplay(kb, helpBindingSelector{action: "focus", args: []string{"down"}}),
			helpBindingDisplay(kb, helpBindingSelector{action: "focus", args: []string{"up"}}),
			helpBindingDisplay(kb, helpBindingSelector{action: "focus", args: []string{"right"}}),
		), "focus"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "display-panes"}), "panes"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "split", args: []string{"root", "v", "--focus"}}), "root-vsplit"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "split", args: []string{"root", "--focus"}}), "root-hsplit"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "split", args: []string{"v", "--focus"}}), "vsplit"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "split", args: []string{"--focus"}}), "hsplit"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "spawn", args: []string{"--spiral"}}), "spiral"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "equalize"}), "equalize"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "kill"}), "kill"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "zoom"}), "zoom"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "undo"}), "undo"),
		helpBarItemForKeys(helpBarJoinParts(
			helpBindingDisplay(kb, helpBindingSelector{action: "swap", args: []string{"backward"}}),
			helpBindingDisplay(kb, helpBindingSelector{action: "swap", args: []string{"forward"}}),
		), "swap"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "copy-mode"}), "copy"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "reload"}), "reload"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "new-window"}), "new-win"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "next-window"}), "next-win"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "prev-window"}), "prev-win"),
		helpBarItemForKeys(helpWindowNumberDisplay(kb), "jump"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "rename-window"}), "rename"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "last-window"}), "last-win"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "toggle-lead"}), "lead"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "detach"}), "detach"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "choose-tree"}), "tree"),
		helpBarItemForKeys(helpBindingDisplay(kb, helpBindingSelector{action: "choose-window"}), "windows"),
	)
	if len(items) == 0 {
		return nil
	}

	return &helpBarState{
		items: items,
	}
}

func compactHelpBarItems(items ...helpBarItem) []helpBarItem {
	result := make([]helpBarItem, 0, len(items))
	for _, item := range items {
		if item.keys == "" || item.desc == "" {
			continue
		}
		result = append(result, item)
	}
	return result
}

func helpBarItemForKeys(keys, desc string) helpBarItem {
	return helpBarItem{keys: keys, desc: desc}
}

func helpBarJoinParts(parts ...string) string {
	return strings.Join(compactHelpKeys(parts...), "/")
}

func compactHelpKeys(keys ...string) []string {
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		result = append(result, key)
	}
	return result
}

const helpBarItemSeparator = "  "

type helpBarLayoutResult struct {
	ends       []int
	maxRowWide int
	totalSlack int
	ok         bool
}

func layoutHelpBarRows(items []helpBarItem, width int) []string {
	if len(items) == 0 || width <= 0 {
		return nil
	}

	segments := make([]string, 0, len(items))
	widths := make([]int, 0, len(items))
	for _, item := range items {
		segment := strings.TrimSpace(item.keys + " " + item.desc)
		if segment == "" {
			continue
		}
		segments = append(segments, segment)
		widths = append(widths, runewidth.StringWidth(segment))
	}
	if len(segments) == 0 {
		return nil
	}

	maxWidth := width
	if maxWidth > 1 {
		maxWidth--
	}
	for targetRows := 2; targetRows <= 4 && targetRows <= len(segments); targetRows++ {
		if rows, ok := partitionHelpBarRows(segments, widths, maxWidth, targetRows); ok {
			return rows
		}
	}
	return greedyHelpBarRows(segments, widths, maxWidth)
}

func partitionHelpBarRows(segments []string, widths []int, maxWidth, targetRows int) ([]string, bool) {
	sepWidth := runewidth.StringWidth(helpBarItemSeparator)
	prefixWidths := make([]int, len(widths)+1)
	for i, width := range widths {
		prefixWidths[i+1] = prefixWidths[i] + width
	}
	rowWidth := func(start, end int) int {
		if start >= end {
			return 0
		}
		return prefixWidths[end] - prefixWidths[start] + sepWidth*(end-start-1)
	}

	type stateKey struct {
		start    int
		rowsLeft int
	}
	memo := make(map[stateKey]helpBarLayoutResult)
	var search func(start, rowsLeft int) helpBarLayoutResult
	search = func(start, rowsLeft int) helpBarLayoutResult {
		key := stateKey{start: start, rowsLeft: rowsLeft}
		if cached, ok := memo[key]; ok {
			return cached
		}

		remaining := len(segments) - start
		if remaining < rowsLeft {
			return helpBarLayoutResult{}
		}
		if rowsLeft == 1 {
			width := rowWidth(start, len(segments))
			if width > maxWidth {
				return helpBarLayoutResult{}
			}
			result := helpBarLayoutResult{
				ends:       []int{len(segments)},
				maxRowWide: width,
				totalSlack: maxWidth - width,
				ok:         true,
			}
			memo[key] = result
			return result
		}

		best := helpBarLayoutResult{}
		maxEnd := len(segments) - rowsLeft + 1
		for end := start + 1; end <= maxEnd; end++ {
			width := rowWidth(start, end)
			if width > maxWidth {
				break
			}
			tail := search(end, rowsLeft-1)
			if !tail.ok {
				continue
			}
			candidate := helpBarLayoutResult{
				ends:       append([]int{end}, tail.ends...),
				maxRowWide: max(width, tail.maxRowWide),
				totalSlack: (maxWidth - width) + tail.totalSlack,
				ok:         true,
			}
			if !best.ok || candidate.maxRowWide < best.maxRowWide ||
				(candidate.maxRowWide == best.maxRowWide && candidate.totalSlack < best.totalSlack) {
				best = candidate
			}
		}
		memo[key] = best
		return best
	}

	best := search(0, targetRows)
	if !best.ok {
		return nil, false
	}

	rows := make([]string, 0, targetRows)
	start := 0
	for _, end := range best.ends {
		rows = append(rows, strings.Join(segments[start:end], helpBarItemSeparator))
		start = end
	}
	return rows, true
}

func greedyHelpBarRows(segments []string, widths []int, maxWidth int) []string {
	if len(segments) == 0 {
		return nil
	}
	sepWidth := runewidth.StringWidth(helpBarItemSeparator)
	rows := make([]string, 0, 4)
	current := segments[0]
	currentWidth := widths[0]
	for i := 1; i < len(segments); i++ {
		width := widths[i]
		if currentWidth+sepWidth+width > maxWidth && current != "" {
			rows = append(rows, current)
			current = segments[i]
			currentWidth = width
			continue
		}
		current += helpBarItemSeparator + segments[i]
		currentWidth += sepWidth + width
	}
	rows = append(rows, current)
	return rows
}

func helpBindingDisplay(kb *config.Keybindings, selector helpBindingSelector) string {
	keys := helpBindingKeys(kb, selector)
	if len(keys) == 0 {
		return ""
	}
	return strings.Join(keys, "/")
}

func helpBindingKeys(kb *config.Keybindings, selector helpBindingSelector) []string {
	if kb == nil {
		return nil
	}
	keys := make([]string, 0, 1)
	for key, binding := range kb.Bindings {
		if binding.Action != selector.action || !slices.Equal(binding.Args, selector.args) {
			continue
		}
		keys = append(keys, formatKeyName(key))
	}
	sort.Strings(keys)
	return keys
}

func helpWindowNumberDisplay(kb *config.Keybindings) string {
	if kb == nil {
		return ""
	}
	numbers := make([]int, 0)
	for _, binding := range kb.Bindings {
		if binding.Action != "select-window" || len(binding.Args) != 1 {
			continue
		}
		n, err := strconv.Atoi(binding.Args[0])
		if err != nil {
			continue
		}
		numbers = append(numbers, n)
	}
	if len(numbers) == 0 {
		return ""
	}
	sort.Ints(numbers)
	numbers = slices.Compact(numbers)
	if len(numbers) == 1 {
		return strconv.Itoa(numbers[0])
	}
	contiguous := true
	for i := 1; i < len(numbers); i++ {
		if numbers[i] != numbers[i-1]+1 {
			contiguous = false
			break
		}
	}
	if contiguous {
		return fmt.Sprintf("%d-%d", numbers[0], numbers[len(numbers)-1])
	}
	parts := make([]string, 0, len(numbers))
	for _, n := range numbers {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, "/")
}

func helpBarConsumedEvents(events []decodedInputEvent, kb *config.Keybindings) int {
	if len(events) == 0 {
		return 0
	}
	if kb == nil {
		return 1
	}
	first, ok := events[0].event.(uv.KeyPressEvent)
	if !ok || !keyPressMatchesByte(first, kb.Prefix) {
		return 1
	}
	if len(events) < 2 {
		return 0
	}
	second, ok := events[1].event.(uv.KeyPressEvent)
	if !ok {
		return 0
	}
	for key, binding := range kb.Bindings {
		if binding.Action == "help" && keyPressMatchesByte(second, key) {
			return 2
		}
	}
	return 0
}

func (cr *ClientRenderer) HelpBarActive() bool {
	return cr.loadState().ui.helpBar != nil
}

func (cr *ClientRenderer) ShowHelpBar(kb *config.Keybindings) bool {
	if cr.renderer.VisibleLayout() == nil {
		return false
	}
	bar := buildHelpBar(kb)
	if bar == nil {
		return false
	}
	result := cr.reduceUI(uiActionShowHelpBar{bar: bar})
	cr.emitUIEvents(result.uiEvents)
	return true
}

func (cr *ClientRenderer) HideHelpBar() bool {
	changed, result := updateClientStateValue(cr, func(next *clientSnapshot) (bool, clientUIResult) {
		changed := next.ui.helpBar != nil
		return changed, next.ui.reduce(uiActionHideHelpBar{})
	})
	cr.emitUIEvents(result.uiEvents)
	return changed
}

func (cr *ClientRenderer) helpBar() *helpBarState {
	return cr.loadState().ui.helpBar
}

func toggleHelpBarOnRenderLoop(cr *ClientRenderer, msgCh chan<- *RenderMsg, kb *config.Keybindings) bool {
	return callLocalRenderAction[bool](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
		if cr.HelpBarActive() {
			cr.HideHelpBar()
			return localRenderResult{
				effects: overlayRenderNowResult().effects,
				value:   true,
			}
		}
		if !cr.ShowHelpBar(kb) {
			return localRenderResult{value: false}
		}
		return localRenderResult{
			effects: overlayRenderNowResult().effects,
			value:   true,
		}
	})
}

func dismissHelpBarOnRenderLoop(cr *ClientRenderer, msgCh chan<- *RenderMsg) bool {
	return callLocalRenderAction[bool](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
		if !cr.HelpBarActive() {
			return localRenderResult{value: false}
		}
		cr.HideHelpBar()
		return localRenderResult{
			effects: overlayRenderNowResult().effects,
			value:   true,
		}
	})
}
