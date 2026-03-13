package pane

import (
	"fmt"
	"sort"
	"strings"

	"github.com/weill-labs/amux/internal/tmux"
)

// Status represents the detected state of an agent pane.
type Status string

const (
	StatusActive       Status = "active"
	StatusIdle         Status = "idle"
	StatusMinimized    Status = "minimized"
	StatusDisconnected Status = "disconnected"
)

// PaneInfo is the enriched view of an amux-managed pane.
type PaneInfo struct {
	ID          string
	Name        string
	Host        string
	Task        string
	Remote      string
	Color       string
	Status      Status
	Output      string // last line of output (for dashboard)
	Minimized   bool
	Height      int
	SessionName string
	WindowIndex string
}

// Discover finds all panes with @amux_name set and enriches them with status.
func Discover(t tmux.Tmux) ([]PaneInfo, error) {
	raw, err := t.ListPanes()
	if err != nil {
		return nil, err
	}

	var panes []PaneInfo
	for _, f := range raw {
		if !f.IsAmux() {
			continue
		}
		p := PaneInfo{
			ID:          f.ID,
			Name:        f.Name,
			Host:        f.Host,
			Task:        f.Task,
			Remote:      f.Remote,
			Color:       f.Color,
			Minimized:   f.Minimized == "1",
			Height:      f.Height,
			SessionName: f.SessionName,
			WindowIndex: f.WindowIndex,
		}

		// Detect status
		switch {
		case p.Minimized:
			p.Status = StatusMinimized
		default:
			out, err := t.PaneOutput(f.ID, 5)
			if err != nil {
				p.Status = StatusDisconnected
			} else {
				p.Output = lastNonEmptyLine(out)
				if IsIdle(out) {
					p.Status = StatusIdle
				} else {
					p.Status = StatusActive
				}
			}
		}

		panes = append(panes, p)
	}

	// Sort by pane ID for deterministic order
	sort.Slice(panes, func(i, j int) bool {
		return panes[i].ID < panes[j].ID
	})

	return panes, nil
}

// IsIdle checks if pane output indicates a shell prompt (agent finished).
// Same heuristic as workforce-sync's checkIdleOutput.
func IsIdle(output string) bool {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		return strings.Contains(trimmed, "❯") ||
			strings.Contains(trimmed, "$ ") ||
			strings.Contains(trimmed, "exited") ||
			strings.HasSuffix(trimmed, "$")
	}
	return false
}

// ResolvePane takes a name or tmux pane ID and returns the tmux pane ID.
// If ref starts with "%" it's treated as a raw pane ID and returned as-is.
// Otherwise it's looked up by @amux_name across all panes.
func ResolvePane(t tmux.Tmux, ref string) (string, error) {
	if strings.HasPrefix(ref, "%") {
		return ref, nil
	}

	raw, err := t.ListPanes()
	if err != nil {
		return "", err
	}

	for _, f := range raw {
		if f.Name == ref {
			return f.ID, nil
		}
	}
	return "", fmt.Errorf("no pane named %q", ref)
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed != "" {
			// Truncate for dashboard display
			if len(trimmed) > 60 {
				return trimmed[:57] + "..."
			}
			return trimmed
		}
	}
	return ""
}
