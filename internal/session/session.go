package session

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const defaultSession = "amux"

// Start creates or attaches to an amux tmux session.
// If the session already exists, it attaches. Otherwise it creates a new one,
// configures amux keybindings and hooks, tags the initial pane, then attaches.
// When detachOthers is true, other clients are detached on attach (like tmux attach -d).
// This function does not return on success — it execs into tmux.
func Start(sessionName string, detachOthers bool) error {
	if sessionName == "" {
		sessionName = defaultSession
	}

	if sessionExists(sessionName) {
		return attach(sessionName, detachOthers)
	}

	// Create new detached session
	cmd := exec.Command("tmux", "new-session", "-d", "-s", sessionName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	// Configure session with amux keybindings, hooks, status bar
	configure(sessionName)

	// Tag the initial pane
	initialPane := firstPane(sessionName)
	if initialPane != "" {
		initPane(sessionName, initialPane)
	}

	return attach(sessionName, detachOthers)
}

func sessionExists(name string) bool {
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil
}

// attach execs into tmux attach, replacing the current process.
// When detachOthers is true, passes -d to detach other clients (like tmux attach -d).
func attach(sessionName string, detachOthers bool) error {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not found: %w", err)
	}

	// If we're already inside tmux, switch client instead of nesting
	if os.Getenv("TMUX") != "" {
		return syscall.Exec(tmuxPath, []string{"tmux", "switch-client", "-t", sessionName}, os.Environ())
	}

	args := []string{"tmux", "attach-session", "-t", sessionName}
	if detachOthers {
		args = []string{"tmux", "attach-session", "-d", "-t", sessionName}
	}
	return syscall.Exec(tmuxPath, args, os.Environ())
}

// firstPane returns the pane ID of the first pane in a session.
func firstPane(sessionName string) string {
	out, err := exec.Command("tmux", "list-panes", "-t", sessionName,
		"-F", "#{pane_id}").Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 0 {
		return lines[0]
	}
	return ""
}

// configure sets up amux-specific keybindings, hooks, and status bar on a session.
func configure(sessionName string) {
	target := func(args ...string) {
		fullArgs := append([]string{"-t", sessionName}, args...)
		// We need to prepend the tmux subcommand
		_ = fullArgs // used below
	}
	_ = target // not used directly, we use tmuxCmd

	// --- Keybindings ---
	// Ctrl-\ → vertical split + auto-tag new pane
	tmuxCmd("bind-key", "-T", "root", `C-\`,
		"split-window", "-h", "-c", "#{pane_current_path}")

	// Ctrl-a - → horizontal split + auto-tag new pane (matches tmux pain-control)
	// These use the global prefix table, which is fine since amux sessions
	// inherit the user's tmux config.

	// --- Hooks ---
	// After any split, auto-tag the new pane with amux metadata.
	// #{pane_id} in hook context refers to the newly created pane.
	amuxBin := amuxPath()
	tmuxCmd("set-hook", "-t", sessionName, "after-split-window",
		fmt.Sprintf("run-shell '%s _init-pane %s #{pane_id}'", amuxBin, sessionName))

	// --- Status bar ---
	// Left: session name with amux branding
	tmuxCmd("set-option", "-t", sessionName, "status-left",
		" #[bold]amux#[nobold] | #S ")
	tmuxCmd("set-option", "-t", sessionName, "status-left-length", "30")

	// Right: pane count + host + time
	tmuxCmd("set-option", "-t", sessionName, "status-right",
		" #{window_panes} panes | #h | %H:%M ")
	tmuxCmd("set-option", "-t", sessionName, "status-right-length", "40")

	// Status bar style — subtle dark
	tmuxCmd("set-option", "-t", sessionName, "status-style", "bg=#313244,fg=#cdd6f4")

	// Pane border — show amux name if set, otherwise dir/branch
	tmuxCmd("set-option", "-t", sessionName, "pane-border-status", "top")
	tmuxCmd("set-option", "-t", sessionName, "pane-border-format",
		"#{?#{@amux_name}, #{pane_id}: #[bold]#{?#{@amux_color},#[fg=#{s/^/#/:@amux_color}],}[#{@amux_name}]#[default] #{@amux_task}, #{pane_id}: #{pane_current_path}}")
}

// initPane sets default amux metadata on a pane.
// Called by the after-split-window hook and during session creation.
func initPane(sessionName, paneID string) {
	counter := nextCounter(sessionName)
	name := fmt.Sprintf("pane-%d", counter)

	for _, kv := range []struct{ key, val string }{
		{"@amux_name", name},
		{"@amux_host", "local"},
	} {
		exec.Command("tmux", "set-option", "-p", "-t", paneID, kv.key, kv.val).Run()
	}
}

// InitPaneFromCLI is the entry point for `amux _init-pane <session> <pane_id>`.
func InitPaneFromCLI(sessionName, paneID string) {
	initPane(sessionName, paneID)
}

// nextCounter reads and increments the session-level @amux_counter.
func nextCounter(sessionName string) int {
	out, err := exec.Command("tmux", "show-options", "-t", sessionName,
		"-v", "@amux_counter").Output()
	current := 0
	if err == nil {
		current, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	}
	next := current + 1
	exec.Command("tmux", "set-option", "-t", sessionName,
		"@amux_counter", strconv.Itoa(next)).Run()
	return next
}

func tmuxCmd(args ...string) error {
	return exec.Command("tmux", args...).Run()
}

// amuxPath returns the path to the amux binary (for use in hooks).
func amuxPath() string {
	path, err := os.Executable()
	if err != nil {
		return "amux" // fall back to PATH lookup
	}
	return path
}
