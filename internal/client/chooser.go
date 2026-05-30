package client

import (
	"io"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/weill-labs/amux/internal/bubblesutil"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

type chooserMode string

const (
	chooserModeTree   chooserMode = "tree"
	chooserModeWindow chooserMode = "window"
	chooserModeRemote chooserMode = "remote"
)

// Source builds chooser rows from a backing pane/window collection.
type Source interface {
	title() string
	buildItems(mode chooserMode, icons render.IconSet) []chooserItem
	toggle(mode chooserMode) *render.ChooserToggle
	toggleMode(mode chooserMode) chooserMode
}

type chooserItem struct {
	text        string
	filterValue string
	selectable  bool
	header      bool // window grouping row in tree mode
	icon        string
	iconColor   string
	textColor   string
	desc        string
	rule        bool
	command     string
	args        []string
}

type chooserState struct {
	mode     chooserMode
	query    bubblesutil.TextInputState
	source   Source
	icons    render.IconSet
	items    []chooserItem
	selected int
}

type localChooserSource struct {
	windows     []proto.WindowSnapshot
	activeWinID uint32
}

type remoteChooserSource struct {
	host   string
	layout *proto.LayoutSnapshot
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
	case chooserModeRemote:
		return "remote-attach"
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
		mode:   mode,
		source: localChooserSource{windows: windows, activeWinID: activeWinID},
		icons:  cr.IconSet(),
	}
	state.rebuild()

	result := cr.reduceUI(uiActionShowChooser{chooser: state})
	cr.emitUIEvents(result.uiEvents)
	return true
}

func (cr *ClientRenderer) ShowRemoteChooser(host string, layout *proto.LayoutSnapshot) bool {
	if strings.TrimSpace(host) == "" || layout == nil {
		return false
	}

	state := &chooserState{
		mode:   chooserModeRemote,
		source: remoteChooserSource{host: host, layout: layout},
		icons:  cr.IconSet(),
	}
	state.rebuild()
	if len(state.items) == 0 {
		return false
	}

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
		case tea.KeyTab, tea.KeyShiftTab:
			cr.toggleChooserMode()
		case tea.KeyRunes:
			// j/k are intentionally NOT movement keys: they must be typeable
			// into the filter query. Use Ctrl+J/Ctrl+K (mapped to Down/Up by
			// normalizeChooserInput) or the arrow keys to move the selection.
			if len(msg.Runes) == 1 && msg.Runes[0] == 'q' && cr.chooserQueryValue() == "" {
				cr.HideChooser()
				return chooserCommand{}
			}
			cr.applyChooserQueryKey(msg)
		default:
			if bubblesutil.IsInlineEditingKey(msg.Type) {
				cr.applyChooserQueryKey(msg)
				continue
			}
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
	title := "Choose"
	var toggle *render.ChooserToggle
	if state.ui.chooser.source != nil {
		title = state.ui.chooser.source.title()
		toggle = state.ui.chooser.source.toggle(state.ui.chooser.mode)
	}
	return &render.ChooserOverlay{
		Title:    title,
		Query:    state.ui.chooser.query.Value,
		Rows:     rows,
		Selected: selected,
		Toggle:   toggle,
	}
}

// toggleChooserMode flips between tree and window views in place, preserving
// the filter query.
func (cr *ClientRenderer) toggleChooserMode() {
	cr.updateState(func(next *clientSnapshot) clientUIResult {
		if next.ui.chooser == nil {
			return clientUIResult{}
		}
		if next.ui.chooser.source == nil {
			return clientUIResult{}
		}
		next.ui.chooser = cloneChooserState(next.ui.chooser)
		nextMode := next.ui.chooser.source.toggleMode(next.ui.chooser.mode)
		if nextMode == next.ui.chooser.mode {
			return clientUIResult{}
		}
		next.ui.chooser.mode = nextMode
		next.ui.chooser.rebuild()
		next.ui.dirty = true
		return clientUIResult{}
	})
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

// selectChooserRowAt moves the selection to absRow (an index into the visible
// rows) and confirms it — used for click-to-choose.
func (cr *ClientRenderer) selectChooserRowAt(absRow int) chooserCommand {
	if cmd := cr.updateChooserSelection(func(model *list.Model) chooserCommand {
		if absRow < 0 || absRow >= len(model.VisibleItems()) {
			return chooserCommand{bell: true}
		}
		model.Select(absRow)
		return chooserCommand{}
	}); cmd.bell {
		// The row went out of range (e.g. the filter changed under us); ring
		// the bell instead of confirming whatever happens to be selected.
		return cmd
	}
	return cr.selectChooser()
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
	if st.source == nil {
		st.items = nil
		st.selected = 0
		return
	}
	st.items = st.source.buildItems(st.mode, st.icons)
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
		selected = chooserSelectableIndex(model.VisibleItems(), selected)
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
			Header:     row.header,
			Icon:       row.icon,
			IconColor:  row.iconColor,
			TextColor:  row.textColor,
			Desc:       row.desc,
			Rule:       row.rule,
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
	// Match the chrome's visible-row window so PgUp/PgDn steps line up with
	// what is actually drawn.
	return render.ChooserRowLimit(screenH)
}

func (s localChooserSource) title() string {
	return "Choose"
}

func (s localChooserSource) buildItems(mode chooserMode, icons render.IconSet) []chooserItem {
	items := make([]chooserItem, 0, len(s.windows)*2)
	treeMode := mode == chooserModeTree
	for _, ws := range s.windows {
		items = append(items, chooserWindowItem(ws, ws.ID == s.activeWinID, treeMode, icons))
		if !treeMode {
			continue
		}
		for _, ps := range ws.Panes {
			items = append(items, chooserPaneItem(ps, ps.ID == ws.ActivePaneID, icons))
		}
	}
	return items
}

func (s localChooserSource) toggle(mode chooserMode) *render.ChooserToggle {
	selected := 0
	if mode == chooserModeWindow {
		selected = 1
	}
	return &render.ChooserToggle{Options: []string{"Tree", "Window"}, Selected: selected}
}

func (s localChooserSource) toggleMode(mode chooserMode) chooserMode {
	if mode == chooserModeWindow {
		return chooserModeTree
	}
	return chooserModeWindow
}

func (s remoteChooserSource) title() string {
	return "Remote panes: " + s.host
}

func (s remoteChooserSource) buildItems(_ chooserMode, icons render.IconSet) []chooserItem {
	if s.layout == nil {
		return nil
	}
	if len(s.layout.Windows) == 0 {
		panes := chooserLeafPanes(s.layout.Root, s.layout.Panes)
		items := make([]chooserItem, 0, len(panes))
		for _, ps := range panes {
			items = append(items, remoteChooserPaneItem(ps, ps.ID == s.layout.ActivePaneID, s.host, "", icons))
		}
		return items
	}

	var items []chooserItem
	for _, ws := range s.layout.Windows {
		panes := chooserLeafPanes(ws.Root, ws.Panes)
		if len(panes) == 0 {
			continue
		}
		items = append(items, remoteChooserWindowHeaderItem(ws, panes))
		for _, ps := range panes {
			items = append(items, remoteChooserPaneItem(ps, ps.ID == ws.ActivePaneID, s.host, ws.Name, icons))
		}
	}
	return items
}

func (s remoteChooserSource) toggle(chooserMode) *render.ChooserToggle {
	return nil
}

func (s remoteChooserSource) toggleMode(mode chooserMode) chooserMode {
	return mode
}

func remoteChooserWindowHeaderItem(ws proto.WindowSnapshot, panes []proto.PaneSnapshot) chooserItem {
	return chooserItem{
		text:        strconv.Itoa(ws.Index) + ":" + chooserWindowName(ws) + " (" + chooserPaneCount(len(panes)) + ")",
		filterValue: strings.ToLower(strings.Join([]string{strconv.Itoa(ws.Index), chooserWindowName(ws)}, " ")),
		header:      true,
		rule:        true,
	}
}

func remoteChooserPaneItem(ps proto.PaneSnapshot, active bool, host, windowName string, icons render.IconSet) chooserItem {
	item := chooserPaneItemWithCommand(ps, active, icons, "remote", []string{"attach", host + ":" + ps.Name})
	filterTerms := append(chooserPaneFilterTerms(ps), host, windowName)
	item.filterValue = strings.ToLower(strings.Join(filterTerms, " "))
	if item.desc == "" {
		item.desc = windowName
	}
	return item
}

func chooserLeafPanes(root proto.CellSnapshot, panes []proto.PaneSnapshot) []proto.PaneSnapshot {
	byID := make(map[uint32]proto.PaneSnapshot, len(panes))
	for _, pane := range panes {
		byID[pane.ID] = pane
	}
	var out []proto.PaneSnapshot
	seen := make(map[uint32]bool, len(panes))
	var walk func(proto.CellSnapshot)
	walk = func(cell proto.CellSnapshot) {
		if cell.IsLeaf {
			if cell.PaneID == 0 || seen[cell.PaneID] {
				return
			}
			seen[cell.PaneID] = true
			if pane, ok := byID[cell.PaneID]; ok {
				out = append(out, pane)
			}
			return
		}
		for _, child := range cell.Children {
			walk(child)
		}
	}
	walk(root)
	return out
}

func chooserWindowFilterValue(ws proto.WindowSnapshot, includePanes bool) string {
	parts := []string{strconv.Itoa(ws.Index), chooserWindowName(ws)}
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

func chooserSelectableIndex(items []list.Item, preferred int) int {
	if len(items) == 0 {
		return 0
	}
	if preferred < 0 {
		preferred = 0
	}
	if preferred >= len(items) {
		preferred = len(items) - 1
	}
	if row, ok := items[preferred].(chooserListItem); ok && row.selectable {
		return preferred
	}
	for i, item := range items {
		row, ok := item.(chooserListItem)
		if ok && row.selectable {
			return i
		}
	}
	return preferred
}

// chooserWindowItem builds the row for a window. In tree mode it is a bold
// section header with a trailing rule; in window mode it is a plain selectable
// row, marked with a status dot when it is the active window.
func chooserWindowItem(ws proto.WindowSnapshot, active, treeMode bool, icons render.IconSet) chooserItem {
	item := chooserItem{
		text:        strconv.Itoa(ws.Index) + ":" + chooserWindowName(ws) + " (" + chooserPaneCount(len(ws.Panes)) + ")",
		filterValue: chooserWindowFilterValue(ws, treeMode),
		selectable:  true,
		command:     "select-window",
		args:        []string{strconv.Itoa(ws.Index)},
	}
	switch {
	case treeMode:
		item.header = true
		item.rule = true
	case active:
		item.icon = icons.PaneActive
		item.iconColor = config.BlueHex
	}
	return item
}

// chooserPaneItem builds the row for a pane: a colored status icon, the pane
// name in its assigned color, and a dim branch/task suffix.
func chooserPaneItem(ps proto.PaneSnapshot, active bool, icons render.IconSet) chooserItem {
	return chooserPaneItemWithCommand(ps, active, icons, "focus", []string{ps.Name})
}

func chooserPaneItemWithCommand(ps proto.PaneSnapshot, active bool, icons render.IconSet, command string, args []string) chooserItem {
	icon := icons.PaneBusy
	switch {
	case ps.Lead:
		icon = icons.PaneLead
	case active:
		icon = icons.PaneActive
	case ps.Idle:
		icon = icons.PaneIdle
	}
	iconColor := ps.Color
	if ps.Idle && !active && !ps.Lead {
		iconColor = config.DimColorHex
	}
	desc := ps.GitBranch
	if desc == "" {
		desc = ps.Task
	}
	return chooserItem{
		text:        ps.Name,
		filterValue: chooserPaneFilterValue(ps),
		selectable:  true,
		icon:        icon,
		iconColor:   iconColor,
		textColor:   ps.Color,
		desc:        desc,
		command:     command,
		args:        append([]string(nil), args...),
	}
}

// chooserPaneCount renders a grammatically correct pane count.
func chooserPaneCount(n int) string {
	if n == 1 {
		return "1 pane"
	}
	return strconv.Itoa(n) + " panes"
}

func chooserWindowName(ws proto.WindowSnapshot) string {
	if ws.Zoomed || ws.ZoomedPaneID != 0 {
		return ws.Name + "Z"
	}
	return ws.Name
}
