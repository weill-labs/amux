package client

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	bubbleshelp "github.com/charmbracelet/bubbles/help"
	bubbleskey "github.com/charmbracelet/bubbles/key"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/render"
)

type helpBindingSelector struct {
	action string
	args   []string
}

type helpBarState struct {
	model    bubbleshelp.Model
	bindings []bubbleskey.Binding
}

func (st *helpBarState) view(width int) string {
	if st == nil {
		return ""
	}
	model := st.model
	if width > 1 {
		model.Width = width - 1
	} else {
		model.Width = width
	}
	return strings.TrimSpace(ansi.Strip(model.ShortHelpView(st.bindings)))
}

func (st *helpBarState) renderOverlay(width int) *render.HelpBarOverlay {
	if st == nil {
		return nil
	}
	view := st.view(width)
	if view == "" {
		return nil
	}
	return &render.HelpBarOverlay{Text: view}
}

func buildHelpBar(kb *config.Keybindings) *helpBarState {
	if kb == nil {
		return nil
	}

	bindings := compactHelpBarBindings(
		helpBarBinding(helpBindingDisplay(kb, helpBindingSelector{action: "help"}), "help"),
		helpBarBinding(helpBarJoinParts(
			helpBarCompactCluster(
				helpBindingDisplay(kb, helpBindingSelector{action: "focus", args: []string{"left"}}),
				helpBindingDisplay(kb, helpBindingSelector{action: "focus", args: []string{"down"}}),
				helpBindingDisplay(kb, helpBindingSelector{action: "focus", args: []string{"up"}}),
				helpBindingDisplay(kb, helpBindingSelector{action: "focus", args: []string{"right"}}),
			),
			helpBindingDisplay(kb, helpBindingSelector{action: "display-panes"}),
		), "nav"),
		helpBarBinding(helpBarJoinParts(
			helpBarCompactCluster(
				helpBindingDisplay(kb, helpBindingSelector{action: "split", args: []string{"root", "v", "--focus"}}),
				helpBindingDisplay(kb, helpBindingSelector{action: "split", args: []string{"--focus"}}),
				helpBindingDisplay(kb, helpBindingSelector{action: "split", args: []string{"v", "--focus"}}),
				helpBindingDisplay(kb, helpBindingSelector{action: "split", args: []string{"root", "--focus"}}),
			),
			helpBindingDisplay(kb, helpBindingSelector{action: "spawn", args: []string{"--spiral"}}),
			helpBindingDisplay(kb, helpBindingSelector{action: "equalize"}),
		), "layout"),
		helpBarBinding(helpBarCompactCluster(
			helpBindingDisplay(kb, helpBindingSelector{action: "kill"}),
			helpBindingDisplay(kb, helpBindingSelector{action: "zoom"}),
			helpBindingDisplay(kb, helpBindingSelector{action: "undo"}),
			helpBindingDisplay(kb, helpBindingSelector{action: "swap", args: []string{"backward"}}),
			helpBindingDisplay(kb, helpBindingSelector{action: "swap", args: []string{"forward"}}),
		), "pane"),
		helpBarBinding(helpBarJoinParts(
			helpBarCompactCluster(
				helpBindingDisplay(kb, helpBindingSelector{action: "new-window"}),
				helpBindingDisplay(kb, helpBindingSelector{action: "next-window"}),
				helpBindingDisplay(kb, helpBindingSelector{action: "prev-window"}),
			),
			helpWindowNumberDisplay(kb),
			helpBindingDisplay(kb, helpBindingSelector{action: "rename-window"}),
			helpBindingDisplay(kb, helpBindingSelector{action: "last-window"}),
		), "wins"),
		helpBarBinding(helpBarJoinParts(
			helpBarCompactCluster(
				helpBindingDisplay(kb, helpBindingSelector{action: "copy-mode"}),
				helpBindingDisplay(kb, helpBindingSelector{action: "reload"}),
				helpBindingDisplay(kb, helpBindingSelector{action: "detach"}),
				helpBindingDisplay(kb, helpBindingSelector{action: "toggle-lead"}),
			),
			helpBindingDisplay(kb, helpBindingSelector{action: "choose-tree"}),
			helpBindingDisplay(kb, helpBindingSelector{action: "choose-window"}),
		), "other"),
	)
	if len(bindings) == 0 {
		return nil
	}

	model := bubbleshelp.New()
	model.ShortSeparator = " • "
	return &helpBarState{
		model:    model,
		bindings: bindings,
	}
}

func compactHelpBarBindings(bindings ...bubbleskey.Binding) []bubbleskey.Binding {
	result := make([]bubbleskey.Binding, 0, len(bindings))
	for _, binding := range bindings {
		if !binding.Enabled() {
			continue
		}
		result = append(result, binding)
	}
	return result
}

func helpBarBinding(keys, desc string) bubbleskey.Binding {
	if keys == "" || desc == "" {
		return bubbleskey.Binding{}
	}
	return bubbleskey.NewBinding(
		bubbleskey.WithKeys(keys),
		bubbleskey.WithHelp(keys, desc),
	)
}

func helpBarCompactCluster(keys ...string) string {
	compact := compactHelpKeys(keys...)
	if len(compact) == 0 {
		return ""
	}
	for _, key := range compact {
		if len([]rune(key)) != 1 {
			return strings.Join(compact, "/")
		}
	}
	return strings.Join(compact, "")
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
