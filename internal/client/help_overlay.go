package client

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/render"
)

const helpOverlayTitle = "keybindings"

type helpOverlayState struct {
	query string
	rows  []render.ChooserOverlayRow
}

type helpBindingSelector struct {
	action string
	args   []string
}

func (st *helpOverlayState) title() string {
	return helpOverlayTitle
}

func (st *helpOverlayState) renderOverlay() *render.ChooserOverlay {
	if st == nil {
		return nil
	}
	rows := make([]render.ChooserOverlayRow, len(st.rows))
	copy(rows, st.rows)
	return &render.ChooserOverlay{
		Title:    st.title(),
		Query:    st.query,
		Rows:     rows,
		Selected: -1,
	}
}

func buildHelpOverlay(kb *config.Keybindings) *helpOverlayState {
	if kb == nil {
		return nil
	}

	categories := []struct {
		title string
		keys  []string
	}{
		{
			title: "Navigation",
			keys: compactHelpKeys(
				helpDirectionalKeys(kb),
				helpDirectionalAlias(kb),
				helpBindingPairDisplay(kb,
					helpBindingSelector{action: "focus", args: []string{"next"}},
					helpBindingSelector{action: "display-panes"},
					", ",
				),
			),
		},
		{
			title: "Layout",
			keys: compactHelpKeys(
				helpBindingDisplay(kb, helpBindingSelector{action: "split", args: []string{"root", "v", "--focus"}}),
				helpBindingDisplay(kb, helpBindingSelector{action: "split", args: []string{"--focus"}}),
				helpBindingDisplay(kb, helpBindingSelector{action: "split", args: []string{"v", "--focus"}}),
				helpBindingDisplay(kb, helpBindingSelector{action: "split", args: []string{"root", "--focus"}}),
				helpBindingDisplay(kb, helpBindingSelector{action: "spawn", args: []string{"--spiral"}}),
				helpBindingDisplay(kb, helpBindingSelector{action: "equalize"}),
			),
		},
		{
			title: "Pane ops",
			keys: compactHelpKeys(
				helpBindingDisplay(kb, helpBindingSelector{action: "kill"}),
				helpBindingDisplay(kb, helpBindingSelector{action: "zoom"}),
				helpBindingDisplay(kb, helpBindingSelector{action: "undo"}),
				helpBindingDisplay(kb, helpBindingSelector{action: "swap", args: []string{"backward"}}),
				helpBindingDisplay(kb, helpBindingSelector{action: "swap", args: []string{"forward"}}),
			),
		},
		{
			title: "Windows",
			keys: compactHelpKeys(
				helpBindingDisplay(kb, helpBindingSelector{action: "new-window"}),
				helpBindingPairDisplay(kb,
					helpBindingSelector{action: "next-window"},
					helpBindingSelector{action: "prev-window"},
					"/",
				),
				helpWindowNumberDisplay(kb),
				helpBindingDisplay(kb, helpBindingSelector{action: "rename-window"}),
				helpBindingDisplay(kb, helpBindingSelector{action: "last-window"}),
			),
		},
		{
			title: "Other",
			keys: compactHelpKeys(
				helpBindingDisplay(kb, helpBindingSelector{action: "copy-mode"}),
				helpBindingDisplay(kb, helpBindingSelector{action: "reload"}),
				helpBindingDisplay(kb, helpBindingSelector{action: "detach"}),
				helpBindingDisplay(kb, helpBindingSelector{action: "toggle-lead"}),
				helpBindingDisplay(kb, helpBindingSelector{action: "choose-tree"}),
				helpBindingDisplay(kb, helpBindingSelector{action: "choose-window"}),
			),
		},
	}

	rows := make([]render.ChooserOverlayRow, 0, len(categories)*2)
	for _, category := range categories {
		if len(category.keys) == 0 {
			continue
		}
		rows = append(rows,
			render.ChooserOverlayRow{Text: category.title, Selectable: true},
			render.ChooserOverlayRow{Text: "  " + strings.Join(category.keys, ", "), Selectable: true},
		)
	}
	if len(rows) == 0 {
		return nil
	}

	queryParts := []string{fmt.Sprintf("Prefix: %s", formatHelpKeyName(kb.Prefix))}
	if helpKey := helpBindingDisplay(kb, helpBindingSelector{action: "help"}); helpKey != "" {
		queryParts = append(queryParts, "Help: "+helpKey)
	}
	queryParts = append(queryParts, "Close: ? / Esc / any key")

	return &helpOverlayState{
		query: strings.Join(queryParts, "  "),
		rows:  rows,
	}
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

func helpBindingDisplay(kb *config.Keybindings, selector helpBindingSelector) string {
	keys := helpBindingKeys(kb, selector)
	if len(keys) == 0 {
		return ""
	}
	return strings.Join(keys, "/")
}

func helpBindingPairDisplay(kb *config.Keybindings, left, right helpBindingSelector, sep string) string {
	leftKey := helpBindingDisplay(kb, left)
	rightKey := helpBindingDisplay(kb, right)
	switch {
	case leftKey == "":
		return rightKey
	case rightKey == "":
		return leftKey
	default:
		return leftKey + sep + rightKey
	}
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

func helpDirectionalKeys(kb *config.Keybindings) string {
	selectors := []helpBindingSelector{
		{action: "focus", args: []string{"left"}},
		{action: "focus", args: []string{"down"}},
		{action: "focus", args: []string{"up"}},
		{action: "focus", args: []string{"right"}},
	}
	keys := make([]string, 0, len(selectors))
	for _, selector := range selectors {
		key := helpBindingDisplay(kb, selector)
		if key == "" {
			continue
		}
		keys = append(keys, key)
	}
	return strings.Join(keys, "/")
}

func helpDirectionalAlias(kb *config.Keybindings) string {
	selectors := []helpBindingSelector{
		{action: "focus", args: []string{"left"}},
		{action: "focus", args: []string{"down"}},
		{action: "focus", args: []string{"up"}},
		{action: "focus", args: []string{"right"}},
	}
	for _, selector := range selectors {
		if helpBindingDisplay(kb, selector) == "" {
			return ""
		}
	}
	return "arrows"
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

func formatHelpKeyName(b byte) string {
	if b >= 1 && b <= 26 {
		return "Ctrl-" + string(rune('a'+b-1))
	}
	return formatKeyName(b)
}

func helpOverlayConsumedEvents(events []decodedInputEvent, kb *config.Keybindings) int {
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

func (cr *ClientRenderer) HelpOverlayActive() bool {
	return cr.loadState().ui.helpOverlay != nil
}

func (cr *ClientRenderer) ShowHelpOverlay(kb *config.Keybindings) bool {
	if cr.renderer.VisibleLayout() == nil {
		return false
	}
	overlay := buildHelpOverlay(kb)
	if overlay == nil {
		return false
	}
	result := cr.reduceUI(uiActionShowHelpOverlay{overlay: overlay})
	cr.emitUIEvents(result.uiEvents)
	return true
}

func (cr *ClientRenderer) HideHelpOverlay() bool {
	changed, result := updateClientStateValue(cr, func(next *clientSnapshot) (bool, clientUIResult) {
		changed := next.ui.helpOverlay != nil
		return changed, next.ui.reduce(uiActionHideHelpOverlay{})
	})
	cr.emitUIEvents(result.uiEvents)
	return changed
}

func (cr *ClientRenderer) helpOverlay() *helpOverlayState {
	return cr.loadState().ui.helpOverlay
}

func toggleHelpOverlayOnRenderLoop(cr *ClientRenderer, msgCh chan<- *RenderMsg, kb *config.Keybindings) bool {
	return callLocalRenderAction[bool](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
		if cr.HelpOverlayActive() {
			cr.HideHelpOverlay()
			return localRenderResult{
				effects: overlayRenderNowResult().effects,
				value:   true,
			}
		}
		if !cr.ShowHelpOverlay(kb) {
			return localRenderResult{value: false}
		}
		return localRenderResult{
			effects: overlayRenderNowResult().effects,
			value:   true,
		}
	})
}

func dismissHelpOverlayOnRenderLoop(cr *ClientRenderer, msgCh chan<- *RenderMsg) bool {
	return callLocalRenderAction[bool](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
		if !cr.HelpOverlayActive() {
			return localRenderResult{value: false}
		}
		cr.HideHelpOverlay()
		return localRenderResult{
			effects: overlayRenderNowResult().effects,
			value:   true,
		}
	})
}
