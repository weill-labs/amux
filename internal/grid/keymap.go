package grid

import tea "github.com/charmbracelet/bubbletea"

type keyBinding struct {
	key  string
	desc string
}

var helpBindings = []keyBinding{
	{"j/k", "navigate"},
	{"Enter", "focus pane"},
	{"m", "minimize/restore"},
	{"J/K", "reorder"},
	{"s", "spawn"},
	{"d", "kill pane"},
	{"R", "reconnect"},
	{"r", "refresh"},
	{"q/Esc", "quit"},
}

func handleKey(msg tea.KeyMsg, m *Model) tea.Cmd {
	switch msg.String() {
	case "q", "esc":
		m.quitting = true
		return tea.Quit

	case "j", "down":
		if m.cursor < len(m.panes)-1 {
			m.cursor++
		}

	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}

	case "enter":
		if m.cursor < len(m.panes) {
			p := m.panes[m.cursor]
			m.tmux.SelectPane(p.ID)
			m.quitting = true
			return tea.Quit
		}

	case "m":
		return m.toggleMinimize()

	case "J", "shift+down":
		return m.swapDown()

	case "K", "shift+up":
		return m.swapUp()

	case "d":
		return m.killPane()

	case "R":
		return m.reconnect()

	case "r":
		return m.refresh()

	case "s":
		// Spawn handled by returning to shell with spawn command
		// For now, just refresh
		return m.refresh()
	}
	return nil
}
