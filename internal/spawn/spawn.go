package spawn

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/tmux"
)

// amuxPath returns the path to the amux binary.
func amuxPath() string {
	path, err := os.Executable()
	if err != nil {
		return "amux"
	}
	return path
}

// SpawnConfig holds parameters for spawning a new agent.
type SpawnConfig struct {
	Name     string // display name for the agent
	Host     string // host key from config, or "local"
	Task     string // issue ID or description
	Repo     string // repository path
	Prompt   string // initial prompt for the agent
	Worktree bool   // create a git worktree
}

// Local spawns a new local agent pane.
func Local(t tmux.Tmux, cfg *config.Config, sc SpawnConfig) (string, error) {
	dir := sc.Repo
	if dir == "" {
		dir = "."
	}

	// Create worktree if requested
	if sc.Worktree && sc.Repo != "" {
		wtName := fmt.Sprintf("amux-%s", sc.Name)
		wtPath := fmt.Sprintf("%s/../%s", sc.Repo, wtName)
		cmd := exec.Command("git", "-C", sc.Repo, "worktree", "add", "-b", wtName, wtPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			// Worktree might already exist — try to use it
			if !strings.Contains(string(out), "already exists") {
				return "", fmt.Errorf("creating worktree: %s", out)
			}
			dir = wtPath
		} else {
			dir = wtPath
		}
	}

	// Build the command to run in the new pane, wrapped with amux _wrap for
	// per-pane status bar support.
	amuxBin := amuxPath()
	agentCmd := "claude"
	if sc.Prompt != "" {
		agentCmd = fmt.Sprintf("claude -p %q", sc.Prompt)
	}
	shellCmd := fmt.Sprintf("cd %s && %s _wrap -- %s", dir, amuxBin, agentCmd)

	// Create new pane
	paneID, err := t.SplitWindow(shellCmd)
	if err != nil {
		return "", fmt.Errorf("creating pane: %w", err)
	}

	// Set metadata
	host := sc.Host
	if host == "" {
		host = "local"
	}
	color := cfg.HostColor(host)

	if err := tmux.SetAmuxMeta(t, paneID, sc.Name, host, sc.Task, "", color); err != nil {
		return paneID, fmt.Errorf("setting metadata: %w", err)
	}

	// Set pane title
	title := fmt.Sprintf("[%s] %s", sc.Name, sc.Task)
	t.SetPaneTitle(paneID, title)

	return paneID, nil
}

// Remote spawns an agent on a remote machine via SSH.
func Remote(t tmux.Tmux, cfg *config.Config, sc SpawnConfig) (string, error) {
	host, ok := cfg.Hosts[sc.Host]
	if !ok {
		return "", fmt.Errorf("unknown host: %s", sc.Host)
	}
	if host.Type != "remote" {
		return "", fmt.Errorf("host %s is not remote", sc.Host)
	}

	user := host.User
	if user == "" {
		user = "ubuntu"
	}
	addr := host.Address
	dir := host.ProjectDir
	if dir == "" {
		dir = "~/Project"
	}

	remoteSession := fmt.Sprintf("amux-%s", sc.Name)

	// Build remote setup script
	var remoteCmd strings.Builder
	remoteCmd.WriteString(fmt.Sprintf("cd %s", dir))

	// Create worktree on remote if requested
	if sc.Worktree {
		wtName := fmt.Sprintf("amux-%s", sc.Name)
		remoteCmd.WriteString(fmt.Sprintf(" && git worktree add -b %s ../%s 2>/dev/null; cd ../%s 2>/dev/null || true",
			wtName, wtName, wtName))
	}

	// Start claude in a remote tmux session
	claudeCmd := "claude"
	if sc.Prompt != "" {
		encoded := base64.StdEncoding.EncodeToString([]byte(sc.Prompt))
		claudeCmd = fmt.Sprintf("claude -p \"$(echo %s | base64 -d)\"", encoded)
	}
	remoteCmd.WriteString(fmt.Sprintf(" && tmux new-session -d -s %s '%s'", remoteSession, claudeCmd))

	// SSH command: setup + attach
	sshCmd := fmt.Sprintf("ssh -t %s@%s '%s && tmux attach -t %s'",
		user, addr, remoteCmd.String(), remoteSession)

	// Create local viewer pane
	paneID, err := t.SplitWindow(sshCmd)
	if err != nil {
		return "", fmt.Errorf("creating viewer pane: %w", err)
	}

	// Set metadata
	color := cfg.HostColor(sc.Host)
	if err := tmux.SetAmuxMeta(t, paneID, sc.Name, sc.Host, sc.Task, remoteSession, color); err != nil {
		return paneID, fmt.Errorf("setting metadata: %w", err)
	}

	title := fmt.Sprintf("[%s] %s@%s", sc.Name, sc.Task, sc.Host)
	t.SetPaneTitle(paneID, title)

	return paneID, nil
}

// Spawn dispatches to Local or Remote based on host config.
func Spawn(t tmux.Tmux, cfg *config.Config, sc SpawnConfig) (string, error) {
	if sc.Host == "" || sc.Host == "local" {
		return Local(t, cfg, sc)
	}
	h, ok := cfg.Hosts[sc.Host]
	if !ok {
		return "", fmt.Errorf("unknown host: %s", sc.Host)
	}
	if h.Type == "local" {
		return Local(t, cfg, sc)
	}
	return Remote(t, cfg, sc)
}
