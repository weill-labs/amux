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
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.ui.chooser != nil
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
	cr.mu.Lock()
	changed := cr.ui.chooser != nil
	result := cr.ui.reduce(uiActionHideChooser{})
	cr.mu.Unlock()
	cr.emitUIEvents(result.uiEvents)
	return changed
}

func (cr *ClientRenderer) HandleChooserInput(raw []byte) chooserCommand {
	if len(raw) == 0 {
		return chooserCommand{}
	}

	cr.mu.Lock()
	state := cr.ui.chooser
	cr.mu.Unlock()
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
			cr.mu.Lock()
			queryEmpty := cr.ui.chooser != nil && cr.ui.chooser.query == ""
			cr.mu.Unlock()
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
	cr.mu.Lock()
	defer cr.mu.Unlock()
	if cr.ui.chooser == nil {
		return nil
	}
	rows := make([]render.ChooserOverlayRow, len(cr.ui.chooser.rows))
	for i, row := range cr.ui.chooser.rows {
		rows[i] = render.ChooserOverlayRow{
			Text:       row.text,
			Selectable: row.selectable,
		}
	}
	return &render.ChooserOverlay{
		Title:    cr.ui.chooser.mode.title(),
		Query:    cr.ui.chooser.query,
		Rows:     rows,
		Selected: cr.ui.chooser.selected,
	}
}

func (cr *ClientRenderer) moveChooser(delta int) chooserCommand {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	if cr.ui.chooser == nil {
		return chooserCommand{}
	}
	next := cr.ui.chooser.selected
	for i := 0; i < len(cr.ui.chooser.rows); i++ {
		next += delta
		if next < 0 {
			next = len(cr.ui.chooser.rows) - 1
		}
		if next >= len(cr.ui.chooser.rows) {
			next = 0
		}
		if cr.ui.chooser.rows[next].selectable {
			cr.ui.chooser.selected = next
			cr.ui.dirty = true
			return chooserCommand{}
		}
	}
	return chooserCommand{bell: true}
}

func (cr *ClientRenderer) editChooserQuery(backspace int, ch byte) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	if cr.ui.chooser == nil {
		return
	}
	if backspace < 0 {
		if len(cr.ui.chooser.query) > 0 {
			cr.ui.chooser.query = cr.ui.chooser.query[:len(cr.ui.chooser.query)-1]
		}
	} else if ch != 0 {
		cr.ui.chooser.query += string(ch)
	}
	cr.ui.chooser.rebuild()
	cr.ui.dirty = true
}

func (cr *ClientRenderer) selectChooser() chooserCommand {
	cr.mu.Lock()
	if cr.ui.chooser == nil || cr.ui.chooser.selected < 0 || cr.ui.chooser.selected >= len(cr.ui.chooser.rows) {
		cr.mu.Unlock()
		return chooserCommand{bell: true}
	}
	row := cr.ui.chooser.rows[cr.ui.chooser.selected]
	if !row.selectable {
		cr.mu.Unlock()
		return chooserCommand{bell: true}
	}
	result := cr.ui.reduce(uiActionHideChooser{})
	cr.mu.Unlock()
	cr.emitUIEvents(result.uiEvents)
	return chooserCommand{command: row.command, args: row.args}
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
