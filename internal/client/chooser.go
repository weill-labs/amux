package client

import (
	"io"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/weill-labs/amux/internal/bubblesutil"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

type chooserMode string

const (
	chooserModeTree   chooserMode = "tree"
	chooserModeWindow chooserMode = "window"
)

type chooserItem struct {
	text        string
	filterValue string
	selectable  bool
	command     string
	args        []string
}

type chooserState struct {
	mode        chooserMode
	query       bubblesutil.TextInputState
	windows     []proto.WindowSnapshot
	activeWinID uint32
	items       []chooserItem
	selected    int
}

type chooserCommand struct {
	command string
	args    []string
	bell    bool
}

type chooserListItem struct {
	chooserItem
}

func (i chooserListItem) FilterValue() string {
	return i.filterValue
}

type chooserListDelegate struct{}

func (chooserListDelegate) Render(io.Writer, list.Model, int, list.Item) {}
func (chooserListDelegate) Height() int                                  { return 1 }
func (chooserListDelegate) Spacing() int                                 { return 0 }
func (chooserListDelegate) Update(tea.Msg, *list.Model) tea.Cmd          { return nil }

func (m chooserMode) title() string {
	switch m {
	case chooserModeTree:
		return "choose-tree"
	case chooserModeWindow:
		return "choose-window"
	default:
		return "chooser"
	}
}

func (m chooserMode) shownEvent() string {
	switch m {
	case chooserModeTree:
		return proto.UIEventChooseTreeShown
	case chooserModeWindow:
		return proto.UIEventChooseWindowShown
	default:
		return ""
	}
}

func (m chooserMode) hiddenEvent() string {
	switch m {
	case chooserModeTree:
		return proto.UIEventChooseTreeHidden
	case chooserModeWindow:
		return proto.UIEventChooseWindowHidden
	default:
		return ""
	}
}

func (cr *ClientRenderer) ChooserActive() bool {
	return cr.loadState().ui.chooser != nil
}

func (cr *ClientRenderer) ShowChooser(mode chooserMode) bool {
	windows, activeWinID := cr.renderer.WindowSnapshots()
	if len(windows) == 0 {
		return false
	}

	state := &chooserState{
		mode:        mode,
		windows:     windows,
		activeWinID: activeWinID,
	}
	state.rebuild()

	result := cr.reduceUI(uiActionShowChooser{chooser: state})
	cr.emitUIEvents(result.uiEvents)
	return true
}

func (cr *ClientRenderer) HideChooser() bool {
	changed, result := updateClientStateValue(cr, func(next *clientSnapshot) (bool, clientUIResult) {
		changed := next.ui.chooser != nil
		return changed, next.ui.reduce(uiActionHideChooser{})
	})
	cr.emitUIEvents(result.uiEvents)
	return changed
}

func (cr *ClientRenderer) HandleChooserInput(raw []byte) chooserCommand {
	if len(raw) == 0 {
		return chooserCommand{}
	}

	state := cr.loadState().ui.chooser
	if state == nil {
		return chooserCommand{}
	}

	result := chooserCommand{}
	for len(raw) > 0 {
		msg, consumed, ok := bubblesutil.DecodeKey(raw)
		if !ok || consumed <= 0 {
			result.bell = true
			raw = raw[1:]
			continue
		}
		raw = raw[consumed:]

		switch msg.Type {
		case tea.KeyEsc:
			cr.HideChooser()
			return chooserCommand{}
		case tea.KeyEnter:
			return cr.selectChooser()
		case tea.KeyUp:
			result = cr.moveChooser(-1)
		case tea.KeyDown:
			result = cr.moveChooser(1)
		case tea.KeyHome:
			result = cr.moveChooserHome()
		case tea.KeyEnd:
			result = cr.moveChooserEnd()
		case tea.KeyPgUp:
			result = cr.pageChooser(-1)
		case tea.KeyPgDown:
			result = cr.pageChooser(1)
		case tea.KeyRunes:
			if len(msg.Runes) == 1 && msg.Runes[0] == 'j' {
				result = cr.moveChooser(1)
				continue
			}
			if len(msg.Runes) == 1 && msg.Runes[0] == 'k' {
				result = cr.moveChooser(-1)
				continue
			}
			if len(msg.Runes) == 1 && msg.Runes[0] == 'q' && cr.chooserQueryValue() == "" {
				cr.HideChooser()
				return chooserCommand{}
			}
			cr.applyChooserQueryKey(msg)
		case tea.KeyLeft, tea.KeyRight, tea.KeyBackspace, tea.KeyDelete,
			tea.KeyCtrlA, tea.KeyCtrlB, tea.KeyCtrlD, tea.KeyCtrlE, tea.KeyCtrlF,
			tea.KeyCtrlK, tea.KeyCtrlN, tea.KeyCtrlP, tea.KeyCtrlU, tea.KeyCtrlW:
			cr.applyChooserQueryKey(msg)
		default:
			result.bell = true
		}
	}
	return result
}

func (cr *ClientRenderer) chooserQueryValue() string {
	state := cr.loadState().ui.chooser
	if state == nil {
		return ""
	}
	return state.query.Value
}

func (cr *ClientRenderer) chooserOverlay() *render.ChooserOverlay {
	return cr.chooserOverlayFromSnapshot(cr.loadState())
}

func (cr *ClientRenderer) chooserOverlayFromSnapshot(state *clientSnapshot) *render.ChooserOverlay {
	if state.ui.chooser == nil {
		return nil
	}
	screenW, screenH := cr.chooserScreenSize()
	rows, selected := state.ui.chooser.overlayRows(screenW, screenH)
	return &render.ChooserOverlay{
		Title:    state.ui.chooser.mode.title(),
		Query:    state.ui.chooser.query.Value,
		Rows:     rows,
		Selected: selected,
	}
}

func (cr *ClientRenderer) moveChooser(delta int) chooserCommand {
	return cr.updateChooserSelection(func(model *list.Model) chooserCommand {
		visible := model.VisibleItems()
		if len(visible) == 0 {
			return chooserCommand{bell: true}
		}
		if !chooserHasSelectableItems(visible) {
			return chooserCommand{bell: true}
		}
		if delta < 0 {
			if model.Index() == 0 {
				model.GoToEnd()
			} else {
				model.CursorUp()
			}
			return chooserCommand{}
		}
		if model.Index() >= len(visible)-1 {
			model.GoToStart()
		} else {
			model.CursorDown()
		}
		return chooserCommand{}
	})
}

func (cr *ClientRenderer) moveChooserHome() chooserCommand {
	return cr.updateChooserSelection(func(model *list.Model) chooserCommand {
		if len(model.VisibleItems()) == 0 {
			return chooserCommand{bell: true}
		}
		model.GoToStart()
		return chooserCommand{}
	})
}

func (cr *ClientRenderer) moveChooserEnd() chooserCommand {
	return cr.updateChooserSelection(func(model *list.Model) chooserCommand {
		if len(model.VisibleItems()) == 0 {
			return chooserCommand{bell: true}
		}
		model.GoToEnd()
		return chooserCommand{}
	})
}

func (cr *ClientRenderer) pageChooser(delta int) chooserCommand {
	return cr.updateChooserSelection(func(model *list.Model) chooserCommand {
		if len(model.VisibleItems()) == 0 {
			return chooserCommand{bell: true}
		}
		if delta < 0 {
			model.PrevPage()
		} else {
			model.NextPage()
		}
		return chooserCommand{}
	})
}

func (cr *ClientRenderer) updateChooserSelection(apply func(*list.Model) chooserCommand) chooserCommand {
	screenW, screenH := cr.chooserScreenSize()
	command, _ := updateClientStateValue(cr, func(next *clientSnapshot) (chooserCommand, clientUIResult) {
		if next.ui.chooser == nil {
			return chooserCommand{}, clientUIResult{}
		}
		next.ui.chooser = cloneChooserState(next.ui.chooser)
		model := next.ui.chooser.model(screenW, screenH)
		command := apply(&model)
		next.ui.chooser.syncFromModel(model)
		next.ui.dirty = true
		return command, clientUIResult{}
	})
	return command
}

func (cr *ClientRenderer) applyChooserQueryKey(msg tea.KeyMsg) {
	cr.updateState(func(next *clientSnapshot) clientUIResult {
		if next.ui.chooser == nil {
			return clientUIResult{}
		}
		next.ui.chooser = cloneChooserState(next.ui.chooser)
		prevValue := next.ui.chooser.query.Value
		next.ui.chooser.query.Update(msg)
		if next.ui.chooser.query.Value != prevValue {
			next.ui.chooser.selected = 0
		}
		next.ui.dirty = true
		return clientUIResult{}
	})
}

func (cr *ClientRenderer) selectChooser() chooserCommand {
	screenW, screenH := cr.chooserScreenSize()
	command, result := updateClientStateValue(cr, func(next *clientSnapshot) (chooserCommand, clientUIResult) {
		if next.ui.chooser == nil {
			return chooserCommand{bell: true}, clientUIResult{}
		}
		model := next.ui.chooser.model(screenW, screenH)
		if len(model.VisibleItems()) == 0 {
			return chooserCommand{bell: true}, clientUIResult{}
		}
		row, ok := model.SelectedItem().(chooserListItem)
		if !ok || !row.selectable {
			return chooserCommand{bell: true}, clientUIResult{}
		}
		return chooserCommand{command: row.command, args: append([]string(nil), row.args...)}, next.ui.reduce(uiActionHideChooser{})
	})
	cr.emitUIEvents(result.uiEvents)
	return command
}

func (st *chooserState) rebuild() {
	items := make([]chooserItem, 0, len(st.windows)*2)
	for _, ws := range st.windows {
		windowItem := chooserItem{
			text:        formatChooserWindowRow(ws, ws.ID == st.activeWinID),
			filterValue: chooserWindowFilterValue(ws, st.mode == chooserModeTree),
			selectable:  true,
			command:     "select-window",
			args:        []string{strconv.Itoa(ws.Index)},
		}
		items = append(items, windowItem)
		if st.mode != chooserModeTree {
			continue
		}
		for _, ps := range ws.Panes {
			items = append(items, chooserItem{
				text:        formatChooserPaneRow(ps, ps.ID == ws.ActivePaneID),
				filterValue: chooserPaneFilterValue(ps),
				selectable:  true,
				command:     "focus",
				args:        []string{ps.Name},
			})
		}
	}
	st.items = items
	st.selected = 0
}

func (st *chooserState) model(screenW, screenH int) list.Model {
	items := make([]list.Item, len(st.items))
	for i, item := range st.items {
		items[i] = chooserListItem{chooserItem: item}
	}
	model := list.New(items, chooserListDelegate{}, screenW, chooserListHeight(screenH))
	model.Filter = list.UnsortedFilter
	model.DisableQuitKeybindings()
	model.SetShowTitle(false)
	model.SetShowFilter(false)
	model.SetShowStatusBar(false)
	model.SetShowPagination(false)
	model.SetShowHelp(false)
	model.FilterInput.Prompt = "> "
	if st.query.Value != "" {
		model.SetFilterText(st.query.Value)
		model.FilterInput.SetCursor(st.query.Cursor)
	}
	if visible := len(model.VisibleItems()); visible > 0 {
		selected := st.selected
		if selected < 0 {
			selected = 0
		}
		if selected >= visible {
			selected = visible - 1
		}
		model.Select(selected)
	}
	return model
}

func (st *chooserState) syncFromModel(model list.Model) {
	st.query.Value = model.FilterInput.Value()
	st.query.Cursor = model.FilterInput.Position()
	visible := model.VisibleItems()
	if len(visible) == 0 {
		st.selected = 0
		return
	}
	selected := model.Index()
	if selected < 0 {
		selected = 0
	}
	if selected >= len(visible) {
		selected = len(visible) - 1
	}
	st.selected = selected
}

func (st *chooserState) overlayRows(screenW, screenH int) ([]render.ChooserOverlayRow, int) {
	model := st.model(screenW, screenH)
	visible := model.VisibleItems()
	if len(visible) == 0 {
		return []render.ChooserOverlayRow{{Text: "No matches", Selectable: false}}, 0
	}
	rows := make([]render.ChooserOverlayRow, 0, len(visible))
	for _, item := range visible {
		row := item.(chooserListItem)
		rows = append(rows, render.ChooserOverlayRow{
			Text:       row.text,
			Selectable: row.selectable,
		})
	}
	return rows, model.Index()
}

func (cr *ClientRenderer) chooserScreenSize() (int, int) {
	layout := cr.VisibleLayout()
	if layout == nil {
		return 80, 24
	}
	return layout.W, layout.H + 1
}

func chooserListHeight(screenH int) int {
	height := screenH - 7
	if height < 1 {
		return 1
	}
	return height
}

func chooserWindowFilterValue(ws proto.WindowSnapshot, includePanes bool) string {
	parts := []string{strconv.Itoa(ws.Index), ws.Name}
	if includePanes {
		for _, ps := range ws.Panes {
			parts = append(parts, chooserPaneFilterTerms(ps)...)
		}
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func chooserPaneFilterValue(ps proto.PaneSnapshot) string {
	return strings.ToLower(strings.Join(chooserPaneFilterTerms(ps), " "))
}

func chooserPaneFilterTerms(ps proto.PaneSnapshot) []string {
	return []string{ps.Name, ps.Host, ps.Task, strconv.Itoa(int(ps.ID))}
}

func chooserHasSelectableItems(items []list.Item) bool {
	for _, item := range items {
		row, ok := item.(chooserListItem)
		if ok && row.selectable {
			return true
		}
	}
	return false
}

func formatChooserWindowRow(ws proto.WindowSnapshot, active bool) string {
	marker := " "
	if active {
		marker = "*"
	}
	return marker + " " + strconv.Itoa(ws.Index) + ":" + ws.Name + " (" + strconv.Itoa(len(ws.Panes)) + " panes)"
}

func formatChooserPaneRow(ps proto.PaneSnapshot, active bool) string {
	marker := " "
	if active {
		marker = "*"
	}
	text := "  " + marker + " " + ps.Name
	if ps.Host != "" && ps.Host != "local" {
		text += " @" + ps.Host
	}
	if ps.Task != "" {
		text += " " + ps.Task
	}
	return text
}
