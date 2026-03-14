package wrap

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/weill-labs/amux/internal/tmux"
)

const statusBg = "#313244" // Catppuccin Mocha surface0 — matches tmux status bar

// StatusBar reads pane metadata from tmux and renders a 1-line ANSI status bar.
type StatusBar struct {
	PaneID string
	T      tmux.Tmux
}

// PaneMeta holds the metadata fields used by the status bar.
type PaneMeta struct {
	Name  string
	Host  string
	Task  string
	Color string
}

// Fetch reads @amux_* metadata for the pane from tmux.
func (sb *StatusBar) Fetch() PaneMeta {
	get := func(key string) string {
		v, _ := sb.T.GetOption(sb.PaneID, key)
		return v
	}
	return PaneMeta{
		Name:  get("@amux_name"),
		Host:  get("@amux_host"),
		Task:  get("@amux_task"),
		Color: get("@amux_color"),
	}
}

// Render produces a 1-line ANSI string with cursor positioning to row.
// Layout: ` ● [name] @host  task ─── HH:MM `
func (sb *StatusBar) Render(cols, row int, meta PaneMeta) string {
	line := sb.RenderLine(cols, meta)
	return fmt.Sprintf("\x1b[%dH%s", row, line)
}

// RenderLine produces the ANSI bar content without cursor positioning.
func (sb *StatusBar) RenderLine(cols int, meta PaneMeta) string {
	bg := lipgloss.Color(statusBg)
	fgText := lipgloss.Color("#cdd6f4") // Catppuccin Mocha text

	accentColor := lipgloss.Color("#89b4fa") // default: blue
	if meta.Color != "" {
		accentColor = lipgloss.Color("#" + meta.Color)
	}

	accent := lipgloss.NewStyle().Foreground(accentColor).Background(bg)
	text := lipgloss.NewStyle().Foreground(fgText).Background(bg)

	icon := accent.Render(" \u25cf") // ●

	var nameStr, hostStr, taskStr string
	if meta.Name != "" {
		nameStr = accent.Bold(true).Render(" [" + meta.Name + "]")
	}
	if meta.Host != "" && meta.Host != "local" {
		hostStr = text.Render(" @" + meta.Host)
	}
	if meta.Task != "" {
		taskStr = text.Render("  " + meta.Task)
	}

	timeStr := text.Render(time.Now().Format("15:04") + " ")

	left := icon + nameStr + hostStr + taskStr
	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(timeStr)
	fillWidth := max(cols-leftWidth-rightWidth, 1)

	fill := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#585b70")). // surface2 -- dim
		Background(bg).
		Render(" " + strings.Repeat("\u2500", fillWidth-1)) // ─

	return left + fill + timeStr
}
