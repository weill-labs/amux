package grid

import (
	"fmt"
	"strings"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/minimize"
	"github.com/weill-labs/amux/internal/pane"
	"github.com/weill-labs/amux/internal/swap"
	"github.com/weill-labs/amux/internal/tmux"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const refreshInterval = 5 * time.Second

type tickMsg time.Time

type refreshMsg struct {
	panes []pane.PaneInfo
	err   error
}

// Model is the bubbletea model for the amux dashboard.
type Model struct {
	tmux     tmux.Tmux
	cfg      *config.Config
	panes    []pane.PaneInfo
	cursor   int
	quitting bool
	width    int
	height   int
	err      error
}

// New creates a new grid model.
func New(t tmux.Tmux, cfg *config.Config) Model {
	return Model{
		tmux: t,
		cfg:  cfg,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.doRefresh(),
		tickCmd(),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		cmd := handleKey(msg, &m)
		return m, cmd

	case tickMsg:
		return m, tea.Batch(m.doRefresh(), tickCmd())

	case refreshMsg:
		m.panes = msg.panes
		m.err = msg.err
		// Clamp cursor
		if m.cursor >= len(m.panes) && len(m.panes) > 0 {
			m.cursor = len(m.panes) - 1
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	// Title
	b.WriteString(titleStyle.Render("amux — Agent Dashboard"))
	b.WriteString("\n\n")

	if m.err != nil {
		b.WriteString(fmt.Sprintf("Error: %v\n", m.err))
		return b.String()
	}

	if len(m.panes) == 0 {
		b.WriteString("  No agent panes found.\n")
		b.WriteString("  Use 'amux spawn' to create one.\n")
	} else {
		// Column widths
		const (
			colStatus = 3
			colName   = 20
			colHost   = 18
			colTask   = 24
			colOutput = 30
		)

		// Header
		header := fmt.Sprintf("  %-*s %-*s %-*s %-*s %s",
			colStatus, "ST",
			colName, "NAME",
			colHost, "HOST",
			colTask, "TASK",
			"OUTPUT")
		b.WriteString(headerStyle.Render(header))
		b.WriteString("\n")

		// Rows
		for i, p := range m.panes {
			status := statusIcon(string(p.Status))
			host := coloredHost(p.Host, p.Color)

			// Truncate fields
			name := truncate(p.Name, colName)
			task := truncate(p.Task, colTask)
			output := truncate(p.Output, colOutput)

			if p.Status == pane.StatusMinimized {
				output = "(minimized)"
			}
			if p.Status == pane.StatusDisconnected {
				output = "(disconnected)"
			}

			row := fmt.Sprintf("  %s %-*s %-*s %-*s %s",
				status,
				colName, name,
				colHost+hostColorPadding(p.Host, host), host,
				colTask, task,
				output)

			if i == m.cursor {
				// Show cursor indicator
				row = "▸" + row[1:]
				b.WriteString(selectedStyle.Render(row))
			} else {
				b.WriteString(normalStyle.Render(row))
			}
			b.WriteString("\n")
		}
	}

	// Help
	b.WriteString("\n")
	var helpParts []string
	for _, kb := range helpBindings {
		helpParts = append(helpParts, fmt.Sprintf("%s:%s", kb.key, kb.desc))
	}
	help := strings.Join(helpParts, "  ")
	b.WriteString(helpStyle.Render(help))

	return lipgloss.NewStyle().MaxWidth(m.width).Render(b.String())
}

// hostColorPadding accounts for ANSI escape codes in colored text.
// lipgloss adds escape sequences that don't take visual space but affect string length.
func hostColorPadding(plain, colored string) int {
	return len(colored) - len(plain)
}

func (m *Model) doRefresh() tea.Cmd {
	return func() tea.Msg {
		panes, err := pane.Discover(m.tmux)
		return refreshMsg{panes: panes, err: err}
	}
}

func (m *Model) toggleMinimize() tea.Cmd {
	if m.cursor >= len(m.panes) {
		return nil
	}
	p := m.panes[m.cursor]
	return func() tea.Msg {
		if p.Minimized {
			minimize.Restore(m.tmux, p.ID)
		} else {
			minimize.Minimize(m.tmux, p.ID)
		}
		panes, err := pane.Discover(m.tmux)
		return refreshMsg{panes: panes, err: err}
	}
}

func (m *Model) swapDown() tea.Cmd {
	if m.cursor >= len(m.panes)-1 {
		return nil
	}
	a := m.panes[m.cursor]
	b := m.panes[m.cursor+1]
	m.cursor++
	return func() tea.Msg {
		swap.SwapWithMeta(m.tmux, a.ID, b.ID)
		panes, err := pane.Discover(m.tmux)
		return refreshMsg{panes: panes, err: err}
	}
}

func (m *Model) swapUp() tea.Cmd {
	if m.cursor <= 0 {
		return nil
	}
	a := m.panes[m.cursor]
	b := m.panes[m.cursor-1]
	m.cursor--
	return func() tea.Msg {
		swap.SwapWithMeta(m.tmux, a.ID, b.ID)
		panes, err := pane.Discover(m.tmux)
		return refreshMsg{panes: panes, err: err}
	}
}

func (m *Model) killPane() tea.Cmd {
	if m.cursor >= len(m.panes) {
		return nil
	}
	p := m.panes[m.cursor]
	return func() tea.Msg {
		m.tmux.KillPane(p.ID)
		panes, err := pane.Discover(m.tmux)
		return refreshMsg{panes: panes, err: err}
	}
}

func (m *Model) reconnect() tea.Cmd {
	if m.cursor >= len(m.panes) {
		return nil
	}
	p := m.panes[m.cursor]
	if p.Status != pane.StatusDisconnected || p.Remote == "" {
		return nil
	}
	return func() tea.Msg {
		host := m.cfg.Hosts[p.Host]
		user := host.User
		if user == "" {
			user = "ubuntu"
		}
		addr := host.Address
		if addr == "" {
			addr = p.Host
		}

		// Check if remote session is alive
		if !m.tmux.RemoteSessionAlive(user, addr, p.Remote) {
			return refreshMsg{panes: m.panes, err: fmt.Errorf("remote session %s not alive on %s", p.Remote, p.Host)}
		}

		// Create new viewer pane
		sshCmd := fmt.Sprintf("ssh -t %s@%s 'tmux attach -t %s'", user, addr, p.Remote)
		newPane, err := m.tmux.SplitWindow(sshCmd)
		if err != nil {
			return refreshMsg{panes: m.panes, err: fmt.Errorf("reconnecting: %w", err)}
		}

		// Copy metadata to new pane
		tmux.SetAmuxMeta(m.tmux, newPane, p.Name, p.Host, p.Task, p.Remote, p.Color)

		panes, err := pane.Discover(m.tmux)
		return refreshMsg{panes: panes, err: err}
	}
}

func (m *Model) refresh() tea.Cmd {
	return m.doRefresh()
}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
