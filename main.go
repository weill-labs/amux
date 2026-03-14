package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/grid"
	"github.com/weill-labs/amux/internal/merge"
	"github.com/weill-labs/amux/internal/minimize"
	"github.com/weill-labs/amux/internal/pane"
	"github.com/weill-labs/amux/internal/session"
	"github.com/weill-labs/amux/internal/spawn"
	swappkg "github.com/weill-labs/amux/internal/swap"
	"github.com/weill-labs/amux/internal/tmux"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	if len(os.Args) < 2 {
		// Default: create or attach to amux tmux session
		if err := session.Start("", false); err != nil {
			fmt.Fprintf(os.Stderr, "amux: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle top-level -d flag: amux -d [session]
	if os.Args[1] == "-d" {
		name := ""
		if len(os.Args) > 2 {
			name = os.Args[2]
		}
		if err := session.Start(name, true); err != nil {
			fmt.Fprintf(os.Stderr, "amux: %v\n", err)
			os.Exit(1)
		}
		return
	}

	switch os.Args[1] {
	case "attach":
		// tmux muscle-memory compat: amux attach [-d] [session]
		name, detach := parseAttachArgs(os.Args[2:])
		if err := session.Start(name, detach); err != nil {
			fmt.Fprintf(os.Stderr, "amux: %v\n", err)
			os.Exit(1)
		}
	case "new":
		// Create a named session: amux new <name>
		name := ""
		if len(os.Args) > 2 {
			name = os.Args[2]
		}
		if err := session.Start(name, false); err != nil {
			fmt.Fprintf(os.Stderr, "amux: %v\n", err)
			os.Exit(1)
		}
	case "adopt":
		requireArg("adopt", 1)
		if err := session.Adopt(os.Args[2]); err != nil {
			fmt.Fprintf(os.Stderr, "amux adopt: %v\n", err)
			os.Exit(1)
		}
	case "dashboard":
		runDashboard()
	case "list":
		runList()
	case "status":
		runStatus()
	case "minimize":
		requireArg("minimize", 2)
		runMinimize(resolveOrDie(os.Args[2]))
	case "restore":
		requireArg("restore", 2)
		runRestore(resolveOrDie(os.Args[2]))
	case "merge":
		requireArg("merge", 2)
		runMerge(os.Args[2], os.Args[3])
	case "swap":
		requireArg("swap", 3)
		runSwap(resolveOrDie(os.Args[2]), resolveOrDie(os.Args[3]))
	case "output":
		requireArg("output", 2)
		runOutput(resolveOrDie(os.Args[2]))
	case "spawn":
		runSpawn(os.Args[2:])
	case "_init-pane":
		// Internal: called by tmux hook to auto-tag new panes
		if len(os.Args) < 4 {
			os.Exit(1)
		}
		session.InitPaneFromCLI(os.Args[2], os.Args[3])
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "amux: unknown command %q\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

// parseAttachArgs parses args for "amux attach [-d] [session]".
func parseAttachArgs(args []string) (sessionName string, detachOthers bool) {
	for _, arg := range args {
		switch arg {
		case "-d":
			detachOthers = true
		default:
			sessionName = arg
		}
	}
	return
}

func requireArg(cmd string, minArgs int) {
	if len(os.Args) < minArgs+1 {
		fmt.Fprintf(os.Stderr, "amux %s: requires %d argument(s)\n", cmd, minArgs)
		os.Exit(1)
	}
}

// resolveOrDie resolves a pane name or ID to a tmux pane ID, exiting on failure.
func resolveOrDie(ref string) string {
	t := newTmux()
	id, err := pane.ResolvePane(t, ref)
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux: %v\n", err)
		os.Exit(1)
	}
	return id
}

func printUsage() {
	fmt.Println(`amux — Agent-Centric Terminal Multiplexer

Usage:
  amux                              Start or attach to amux session
  amux -d [session]                 Attach and detach other clients
  amux attach [-d] [session]        Attach (tmux muscle-memory compat)
  amux new [name]                   Start a new named session
  amux adopt <session>              Adopt an existing tmux session
  amux dashboard                    Open TUI dashboard (in popup or standalone)
  amux list                         List agent panes with metadata
  amux status                       Show agent status (for scripts/prompts)
  amux output <pane>                Show pane output (last 50 lines)
  amux minimize <pane>              Minimize a pane to 1 row
  amux restore <pane>               Restore a minimized pane
  amux merge <src_win> <dst_win>    Merge panes from one window into another
  amux swap <pane_a> <pane_b>       Swap two panes (with metadata)
  amux spawn [flags]                Spawn a new agent

Panes can be referenced by name (pane-3) or tmux ID (%40).

Inside an amux session (prefix is Ctrl-a):
  Ctrl-\                            Split pane vertically (new shell)
  prefix g                          Open dashboard popup
  prefix a m                        Minimize current pane
  prefix a r                        Restore current pane
  prefix a s                        Spawn agent (interactive)
  prefix a l                        List agent panes

Spawn flags:
  --name NAME          Agent display name (required)
  --host HOST          Host key from config, or "local" (default: local)
  --task TASK          Issue ID or description
  --repo PATH          Repository path
  --prompt PROMPT      Initial prompt for the agent
  --worktree           Create a git worktree`)
}

func newTmux() tmux.Tmux {
	return &tmux.LiveTmux{}
}

func loadConfig() *config.Config {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux: warning: %v\n", err)
		return &config.Config{Hosts: make(map[string]config.Host)}
	}
	return cfg
}

func runDashboard() {
	t := newTmux()
	cfg := loadConfig()
	m := grid.New(t, cfg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "amux: %v\n", err)
		os.Exit(1)
	}
}

func runList() {
	t := newTmux()
	panes, err := pane.Discover(t)
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux list: %v\n", err)
		os.Exit(1)
	}
	if len(panes) == 0 {
		fmt.Println("No agent panes found.")
		return
	}
	fmt.Printf("%-6s %-20s %-15s %-12s %-25s %s\n",
		"PANE", "NAME", "HOST", "STATUS", "TASK", "OUTPUT")
	for _, p := range panes {
		output := p.Output
		if p.Status == pane.StatusMinimized {
			output = "(minimized)"
		}
		if p.Status == pane.StatusDisconnected {
			output = "(disconnected)"
		}
		fmt.Printf("%-6s %-20s %-15s %-12s %-25s %s\n",
			p.ID, p.Name, p.Host, p.Status, p.Task, output)
	}
}

func runStatus() {
	t := newTmux()
	panes, err := pane.Discover(t)
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux status: %v\n", err)
		os.Exit(1)
	}
	active := 0
	idle := 0
	minimized := 0
	disconnected := 0
	for _, p := range panes {
		switch p.Status {
		case pane.StatusActive:
			active++
		case pane.StatusIdle:
			idle++
		case pane.StatusMinimized:
			minimized++
		case pane.StatusDisconnected:
			disconnected++
		}
	}
	fmt.Printf("agents: %d total, %d active, %d idle, %d minimized, %d disconnected\n",
		len(panes), active, idle, minimized, disconnected)
}

func runOutput(paneID string) {
	t := newTmux()
	out, err := t.PaneOutput(paneID, 50)
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux output: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(out)
}

func runMinimize(paneID string) {
	t := newTmux()
	if err := minimize.Minimize(t, paneID); err != nil {
		fmt.Fprintf(os.Stderr, "amux minimize: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Minimized %s\n", paneID)
}

func runRestore(paneID string) {
	t := newTmux()
	if err := minimize.Restore(t, paneID); err != nil {
		fmt.Fprintf(os.Stderr, "amux restore: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Restored %s\n", paneID)
}

func runMerge(srcWindow, dstWindow string) {
	t := newTmux()
	count, err := merge.Merge(t, srcWindow, dstWindow)
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux merge: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Merged %d panes from window %s into window %s\n", count, srcWindow, dstWindow)
}

func runSwap(paneA, paneB string) {
	t := newTmux()
	if err := swappkg.SwapWithMeta(t, paneA, paneB); err != nil {
		fmt.Fprintf(os.Stderr, "amux swap: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Swapped %s <-> %s\n", paneA, paneB)
}

func runSpawn(args []string) {
	sc := spawn.SpawnConfig{}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			i++
			if i < len(args) {
				sc.Name = args[i]
			}
		case "--host":
			i++
			if i < len(args) {
				sc.Host = args[i]
			}
		case "--task":
			i++
			if i < len(args) {
				sc.Task = args[i]
			}
		case "--repo":
			i++
			if i < len(args) {
				sc.Repo = args[i]
			}
		case "--prompt":
			i++
			if i < len(args) {
				sc.Prompt = args[i]
			}
		case "--worktree":
			sc.Worktree = true
		default:
			if !strings.HasPrefix(args[i], "-") && sc.Name == "" {
				sc.Name = args[i]
			} else {
				fmt.Fprintf(os.Stderr, "amux spawn: unknown flag %q\n", args[i])
				os.Exit(1)
			}
		}
	}

	if sc.Name == "" {
		fmt.Fprintf(os.Stderr, "amux spawn: --name is required\n")
		os.Exit(1)
	}

	t := newTmux()
	cfg := loadConfig()
	paneID, err := spawn.Spawn(t, cfg, sc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux spawn: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Spawned %s in %s\n", sc.Name, paneID)
}
