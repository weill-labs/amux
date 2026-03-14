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
	amuxBin := amuxPath()

	// opt sets a session-scoped tmux option.
	opt := func(key, val string) {
		tmuxCmd("set-option", "-t", sessionName, key, val)
	}
	// popup binds a key in the amux keytable to an amux subcommand in a popup.
	popup := func(key, w, h, subcmd string) {
		tmuxCmd("bind-key", "-T", "amux", key,
			"display-popup", "-E", "-w", w, "-h", h,
			amuxBin+" "+subcmd)
	}

	// --- Prefix ---
	// amux owns C-a as prefix; raw tmux sessions keep default C-b.
	// C-a C-a sends literal C-a to the shell (readline beginning-of-line).
	opt("prefix", "C-a")
	tmuxCmd("bind-key", "C-a", "send-prefix")

	// --- Agent optimizations ---
	opt("history-limit", "50000") // large scrollback for `amux output`

	// OSC 52 clipboard — makes copy work over SSH and nested tmux.
	tmuxCmd("set-option", "-g", "set-clipboard", "on")
	tmuxCmd("set-option", "-g", "allow-passthrough", "on")

	// --- Keybindings ---
	// Note: tmux keybindings are global (no per-session scoping), but they're
	// effectively unreachable from non-amux sessions which use a different prefix.

	// Ctrl-\ → vertical split (inherits current directory)
	tmuxCmd("bind-key", "-T", "root", `C-\`,
		"split-window", "-h", "-c", "#{pane_current_path}")

	// Standard splits/windows inherit current directory
	tmuxCmd("bind-key", `"`, "split-window", "-c", "#{pane_current_path}")
	tmuxCmd("bind-key", "%", "split-window", "-h", "-c", "#{pane_current_path}")
	tmuxCmd("bind-key", "c", "new-window", "-c", "#{pane_current_path}")

	// prefix+g → dashboard popup (direct shortcut)
	tmuxCmd("bind-key", "g",
		"display-popup", "-E", "-w", "80%", "-h", "80%",
		amuxBin+" dashboard")

	// prefix+a → enter amux keytable for single-key dispatch
	tmuxCmd("bind-key", "a", "switch-client", "-T", "amux")

	// amux keytable: prefix+a then one of these keys
	popup("g", "80%", "80%", "dashboard")
	popup("s", "60%", "40%", "spawn -i")
	popup("l", "60%", "40%", "list")
	tmuxCmd("bind-key", "-T", "amux", "m",
		"run-shell", fmt.Sprintf("%s minimize #{pane_id}", amuxBin))
	tmuxCmd("bind-key", "-T", "amux", "r",
		"run-shell", fmt.Sprintf("%s restore #{pane_id}", amuxBin))

	// --- Hooks ---
	// After any split, auto-tag the new pane with amux metadata.
	// #{pane_id} in hook context refers to the newly created pane.
	tmuxCmd("set-hook", "-t", sessionName, "after-split-window",
		fmt.Sprintf("run-shell '%s _init-pane %s #{pane_id}'", amuxBin, sessionName))

	// Mouse tracking reset on reattach — fixes stale SGR mouse reports
	// after SSH disconnect while a TUI had mouse tracking enabled.
	tmuxCmd("set-hook", "-t", sessionName, "client-attached",
		`run-shell "tmux set -g mouse off && tmux set -g mouse on"`)

	// --- Status bar ---
	opt("status-left", " #[bold]amux#[nobold] | #S ")
	opt("status-left-length", "30")
	opt("status-right", " #{window_panes} panes | #h | %H:%M ")
	opt("status-right-length", "40")
	opt("status-style", "bg=#313244,fg=#cdd6f4") // Catppuccin Mocha surface0/text

	// Pane border — show amux name+task if set, otherwise dir/branch
	opt("pane-border-status", "top")
	opt("pane-border-format",
		"#{?#{@amux_name},"+
			" #{pane_id}: #[bold]#{?#{@amux_color},#[fg=#{s/^/#/:@amux_color}],}"+
			"[#{@amux_name}]#[default] #{@amux_task},"+
			" #{pane_id}: #[bold,fg=#89b4fa]#{@pane_dir}#[nobold,fg=#f9e2af]#{@pane_branch}#[default]} ")
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
