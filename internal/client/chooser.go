package client

import (
	"strconv"
	"strings"

	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

type chooserMode string

const (
	chooserModeTree   chooserMode = "tree"
	chooserModeWindow chooserMode = "window"
)

type chooserItem struct {
	text       string
	selectable bool
	command    string
	args       []string
}

type chooserState struct {
	mode        chooserMode
	query       string
	windows     []proto.WindowSnapshot
	activeWinID uint32
	rows        []chooserItem
	selected    int
}

type chooserCommand struct {
	command string
	args    []string
	bell    bool
}

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

	if len(raw) == 3 && raw[0] == 0x1b && raw[1] == '[' {
		switch raw[2] {
		case 'A':
			return cr.moveChooser(-1)
		case 'B':
			return cr.moveChooser(1)
		}
	}

	var result chooserCommand
	for _, b := range raw {
		switch {
		case b == 0x1b:
			cr.HideChooser()
			return chooserCommand{}
		case b == '\r' || b == '\n':
			return cr.selectChooser()
		case b == 0x7f || b == 0x08:
			cr.editChooserQuery(-1, 0)
		case b == 'j':
			result = cr.moveChooser(1)
		case b == 'k':
			result = cr.moveChooser(-1)
		case b == 'q':
			queryEmpty := false
			if state := cr.loadState().ui.chooser; state != nil {
				queryEmpty = state.query == ""
			}
			if queryEmpty {
				cr.HideChooser()
				return chooserCommand{}
			}
			cr.editChooserQuery(0, b)
		case b >= 0x20 && b <= 0x7e:
			cr.editChooserQuery(0, b)
		default:
			result.bell = true
		}
	}
	return result
}

func (cr *ClientRenderer) chooserOverlay() *render.ChooserOverlay {
	return cr.chooserOverlayFromSnapshot(cr.loadState())
}

func (cr *ClientRenderer) chooserOverlayFromSnapshot(state *clientSnapshot) *render.ChooserOverlay {
	if state.ui.chooser == nil {
		return nil
	}
	rows := make([]render.ChooserOverlayRow, len(state.ui.chooser.rows))
	for i, row := range state.ui.chooser.rows {
		rows[i] = render.ChooserOverlayRow{
			Text:       row.text,
			Selectable: row.selectable,
		}
	}
	return &render.ChooserOverlay{
		Title:    state.ui.chooser.mode.title(),
		Query:    state.ui.chooser.query,
		Rows:     rows,
		Selected: state.ui.chooser.selected,
	}
}

func (cr *ClientRenderer) moveChooser(delta int) chooserCommand {
	command, _ := updateClientStateValue(cr, func(next *clientSnapshot) (chooserCommand, clientUIResult) {
		if next.ui.chooser == nil {
			return chooserCommand{}, clientUIResult{}
		}
		next.ui.chooser = cloneChooserState(next.ui.chooser)
		selected := next.ui.chooser.selected
		for i := 0; i < len(next.ui.chooser.rows); i++ {
			selected += delta
			if selected < 0 {
				selected = len(next.ui.chooser.rows) - 1
			}
			if selected >= len(next.ui.chooser.rows) {
				selected = 0
			}
			if next.ui.chooser.rows[selected].selectable {
				next.ui.chooser.selected = selected
				next.ui.dirty = true
				return chooserCommand{}, clientUIResult{}
			}
		}
		return chooserCommand{bell: true}, clientUIResult{}
	})
	return command
}

func (cr *ClientRenderer) editChooserQuery(backspace int, ch byte) {
	cr.updateState(func(next *clientSnapshot) clientUIResult {
		if next.ui.chooser == nil {
			return clientUIResult{}
		}
		next.ui.chooser = cloneChooserState(next.ui.chooser)
		if backspace < 0 {
			if len(next.ui.chooser.query) > 0 {
				next.ui.chooser.query = next.ui.chooser.query[:len(next.ui.chooser.query)-1]
			}
		} else if ch != 0 {
			next.ui.chooser.query += string(ch)
		}
		next.ui.chooser.rebuild()
		next.ui.dirty = true
		return clientUIResult{}
	})
}

func (cr *ClientRenderer) selectChooser() chooserCommand {
	command, result := updateClientStateValue(cr, func(next *clientSnapshot) (chooserCommand, clientUIResult) {
		if next.ui.chooser == nil || next.ui.chooser.selected < 0 || next.ui.chooser.selected >= len(next.ui.chooser.rows) {
			return chooserCommand{bell: true}, clientUIResult{}
		}
		row := next.ui.chooser.rows[next.ui.chooser.selected]
		if !row.selectable {
			return chooserCommand{bell: true}, clientUIResult{}
		}
		return chooserCommand{command: row.command, args: row.args}, next.ui.reduce(uiActionHideChooser{})
	})
	cr.emitUIEvents(result.uiEvents)
	return command
}

func (st *chooserState) rebuild() {
	query := strings.ToLower(st.query)
	rows := make([]chooserItem, 0, len(st.windows)*2)
	for _, ws := range st.windows {
		windowText := formatChooserWindowRow(ws, ws.ID == st.activeWinID)
		windowItem := chooserItem{
			text:       windowText,
			selectable: true,
			command:    "select-window",
			args:       []string{strconv.Itoa(ws.Index)},
		}
		windowMatches := chooserWindowMatches(ws, query)
		if st.mode == chooserModeWindow {
			if query == "" || windowMatches {
				rows = append(rows, windowItem)
			}
			continue
		}

		var paneRows []chooserItem
		for _, ps := range ws.Panes {
			if query != "" && !windowMatches && !chooserPaneMatches(ps, query) {
				continue
			}
			paneRows = append(paneRows, chooserItem{
				text:       formatChooserPaneRow(ps, ps.ID == ws.ActivePaneID),
				selectable: true,
				command:    "focus",
				args:       []string{ps.Name},
			})
		}
		if query == "" || windowMatches || len(paneRows) > 0 {
			rows = append(rows, windowItem)
			rows = append(rows, paneRows...)
		}
	}
	if len(rows) == 0 {
		rows = []chooserItem{{text: "No matches", selectable: false}}
		st.selected = 0
		st.rows = rows
		return
	}
	st.rows = rows
	if st.selected >= len(st.rows) || !st.rows[st.selected].selectable {
		st.selected = 0
		for i, row := range st.rows {
			if row.selectable {
				st.selected = i
				break
			}
		}
	}
}

func chooserWindowMatches(ws proto.WindowSnapshot, query string) bool {
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(strconv.Itoa(ws.Index)+" "+ws.Name), query)
}

func chooserPaneMatches(ps proto.PaneSnapshot, query string) bool {
	if query == "" {
		return true
	}
	fields := []string{ps.Name, ps.Host, ps.Task, strconv.Itoa(int(ps.ID))}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
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
