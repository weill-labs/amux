package tmux

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Tmux abstracts tmux operations for testing.
type Tmux interface {
	// ListPanes returns all panes with their @amux_* metadata.
	// Keys are pane IDs (e.g., "%5").
	ListPanes() (map[string]PaneFields, error)

	// PaneOutput returns the last N lines of visible pane content.
	PaneOutput(paneID string, lines int) (string, error)

	// ResizePane resizes a pane to the given height.
	ResizePane(paneID string, height int) error

	// SwapPane swaps two panes' content (not metadata).
	SwapPane(src, dst string) error

	// PaneHeight returns the current height of a pane.
	PaneHeight(paneID string) (int, error)

	// GetOption reads a pane-level user option.
	GetOption(paneID, key string) (string, error)

	// SetOption sets a pane-level user option.
	SetOption(paneID, key, value string) error

	// SetPaneTitle sets the pane title.
	SetPaneTitle(paneID, title string) error

	// SelectPane focuses a pane.
	SelectPane(paneID string) error

	// KillPane kills a pane.
	KillPane(paneID string) error

	// SplitWindow creates a new pane running cmd, returns the new pane ID.
	SplitWindow(cmd string) (string, error)

	// SendKeys sends keystrokes to a pane.
	SendKeys(paneID string, keys ...string) error

	// CurrentSession returns the current tmux session name.
	CurrentSession() string

	// RemoteSessionAlive checks if a remote tmux session exists via SSH.
	RemoteSessionAlive(user, host, session string) bool

	// WindowPanes returns pane IDs in the same window as the given pane.
	WindowPanes(paneID string) ([]string, error)

	// JoinPane moves src pane to be adjacent to dst pane (in dst's window).
	JoinPane(src, dst string) error

	// SessionWindowPanes returns pane IDs in a specific session:window.
	SessionWindowPanes(sessionWindow string) ([]string, error)
}

// PaneFields holds raw tmux fields for a single pane.
type PaneFields struct {
	ID          string
	SessionName string
	WindowIndex string
	Height      int

	// @amux_* metadata
	Name      string // @amux_name
	Host      string // @amux_host
	Task      string // @amux_task
	Remote    string // @amux_remote
	Color     string // @amux_color
	Minimized string // @amux_minimized ("1" or "")
	RestoreH  string // @amux_restore_h
}

// IsAmux returns true if this pane has amux metadata.
func (p PaneFields) IsAmux() bool {
	return p.Name != ""
}

// LiveTmux implements Tmux using real tmux commands.
type LiveTmux struct{}

// validName matches safe tmux session names and SSH identifiers.
var validName = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func (t *LiveTmux) ListPanes() (map[string]PaneFields, error) {
	format := strings.Join([]string{
		"#{pane_id}",
		"#{session_name}",
		"#{window_index}",
		"#{pane_height}",
		"#{@amux_name}",
		"#{@amux_host}",
		"#{@amux_task}",
		"#{@amux_remote}",
		"#{@amux_color}",
		"#{@amux_minimized}",
		"#{@amux_restore_h}",
	}, "\t")

	out, err := exec.Command("tmux", "list-panes", "-a", "-F", format).Output()
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %w", err)
	}

	panes := make(map[string]PaneFields)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 11)
		if len(fields) < 4 {
			continue
		}

		height, _ := strconv.Atoi(fields[3])
		p := PaneFields{
			ID:          fields[0],
			SessionName: fields[1],
			WindowIndex: fields[2],
			Height:      height,
		}
		if len(fields) > 4 {
			p.Name = fields[4]
		}
		if len(fields) > 5 {
			p.Host = fields[5]
		}
		if len(fields) > 6 {
			p.Task = fields[6]
		}
		if len(fields) > 7 {
			p.Remote = fields[7]
		}
		if len(fields) > 8 {
			p.Color = fields[8]
		}
		if len(fields) > 9 {
			p.Minimized = fields[9]
		}
		if len(fields) > 10 {
			p.RestoreH = fields[10]
		}

		panes[p.ID] = p
	}
	return panes, nil
}

func (t *LiveTmux) PaneOutput(paneID string, lines int) (string, error) {
	out, err := exec.Command("tmux", "capture-pane", "-t", paneID, "-p", "-S",
		fmt.Sprintf("-%d", lines)).Output()
	return string(out), err
}

func (t *LiveTmux) ResizePane(paneID string, height int) error {
	return exec.Command("tmux", "resize-pane", "-t", paneID, "-y",
		strconv.Itoa(height)).Run()
}

func (t *LiveTmux) SwapPane(src, dst string) error {
	return exec.Command("tmux", "swap-pane", "-s", src, "-t", dst).Run()
}

func (t *LiveTmux) PaneHeight(paneID string) (int, error) {
	out, err := exec.Command("tmux", "display-message", "-t", paneID, "-p",
		"#{pane_height}").Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

func (t *LiveTmux) GetOption(paneID, key string) (string, error) {
	out, err := exec.Command("tmux", "show-options", "-p", "-t", paneID, "-v", key).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (t *LiveTmux) SetOption(paneID, key, value string) error {
	return exec.Command("tmux", "set-option", "-p", "-t", paneID, key, value).Run()
}

func (t *LiveTmux) SetPaneTitle(paneID, title string) error {
	return exec.Command("tmux", "select-pane", "-t", paneID, "-T", title).Run()
}

func (t *LiveTmux) SelectPane(paneID string) error {
	return exec.Command("tmux", "select-pane", "-t", paneID).Run()
}

func (t *LiveTmux) KillPane(paneID string) error {
	return exec.Command("tmux", "kill-pane", "-t", paneID).Run()
}

func (t *LiveTmux) SplitWindow(cmd string) (string, error) {
	out, err := exec.Command("tmux", "split-window", "-h", "-P", "-F",
		"#{pane_id}", cmd).Output()
	return strings.TrimSpace(string(out)), err
}

func (t *LiveTmux) SendKeys(paneID string, keys ...string) error {
	args := append([]string{"send-keys", "-t", paneID}, keys...)
	return exec.Command("tmux", args...).Run()
}

func (t *LiveTmux) CurrentSession() string {
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (t *LiveTmux) RemoteSessionAlive(user, host, session string) bool {
	if !validName.MatchString(session) || !validName.MatchString(user) || !validName.MatchString(host) {
		return false
	}
	script := fmt.Sprintf(
		`tmux has-session -t %s 2>/dev/null && echo ALIVE && exit 0; `+
			`tmux -S ~/.tmux/tmp/tmux-$(id -u)/default has-session -t %s 2>/dev/null && echo ALIVE`,
		session, session)
	out, err := exec.Command("ssh", "-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=accept-new",
		fmt.Sprintf("%s@%s", user, host), script).Output()
	if err != nil && len(out) == 0 {
		return false
	}
	return strings.Contains(string(out), "ALIVE")
}

func (t *LiveTmux) WindowPanes(paneID string) ([]string, error) {
	// Get the window of this pane, then list all panes in that window
	out, err := exec.Command("tmux", "display-message", "-t", paneID, "-p",
		"#{session_name}:#{window_index}").Output()
	if err != nil {
		return nil, err
	}
	window := strings.TrimSpace(string(out))

	out, err = exec.Command("tmux", "list-panes", "-t", window, "-F", "#{pane_id}").Output()
	if err != nil {
		return nil, err
	}

	var panes []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			panes = append(panes, line)
		}
	}
	return panes, nil
}

func (t *LiveTmux) JoinPane(src, dst string) error {
	return exec.Command("tmux", "join-pane", "-s", src, "-t", dst).Run()
}

func (t *LiveTmux) SessionWindowPanes(sessionWindow string) ([]string, error) {
	out, err := exec.Command("tmux", "list-panes", "-t", sessionWindow, "-F", "#{pane_id}").Output()
	if err != nil {
		return nil, err
	}
	var panes []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			panes = append(panes, line)
		}
	}
	return panes, nil
}

// AmuxOptions is the list of all @amux_* option keys.
var AmuxOptions = []string{
	"@amux_name",
	"@amux_host",
	"@amux_task",
	"@amux_remote",
	"@amux_color",
	"@amux_minimized",
	"@amux_restore_h",
}

// SetAmuxMeta sets all @amux_* metadata on a pane.
func SetAmuxMeta(t Tmux, paneID, name, host, task, remote, color string) error {
	opts := map[string]string{
		"@amux_name":   name,
		"@amux_host":   host,
		"@amux_task":   task,
		"@amux_remote": remote,
		"@amux_color":  color,
	}
	for k, v := range opts {
		if v == "" {
			continue
		}
		if err := t.SetOption(paneID, k, v); err != nil {
			return fmt.Errorf("setting %s on %s: %w", k, paneID, err)
		}
	}
	return nil
}
