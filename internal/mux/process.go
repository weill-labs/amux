package mux

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// processTimeout limits how long pgrep/ps subprocess calls can take.
const processTimeout = 500 * time.Millisecond

// AgentStatus holds the process-level status of a pane for JSON capture.
type AgentStatus struct {
	Idle           bool
	IdleSince      time.Time
	CurrentCommand string
	ChildPIDs      []int
}

// AgentStatus inspects the pane's process tree and returns its current status.
// Uses pgrep/ps for portable macOS+Linux support. Safe to call without
// holding any session-level locks — only acquires the pane's internal idleMu.
//
// When idle, CurrentCommand reports the shell name (e.g., "bash").
// When busy, CurrentCommand reports the foreground child's name.
func (p *Pane) AgentStatus() AgentStatus {
	shellPid := p.ProcessPid()
	if shellPid == 0 {
		// Dead or restored pane with no process — report idle since creation
		return AgentStatus{
			Idle:      true,
			IdleSince: p.createdAt,
			ChildPIDs: []int{},
		}
	}

	children := childPIDs(shellPid)

	// If pgrep returned empty but the pane was recently busy (within 500ms),
	// retry once — pgrep can miss recently-forked children under load.
	// Skip retry for panes that have been idle longer to avoid catching
	// transient shell children during prompt processing.
	if len(children) == 0 {
		p.idleMu.Lock()
		recentlyBusy := !p.lastBusySeen.IsZero() && time.Since(p.lastBusySeen) < 500*time.Millisecond
		p.idleMu.Unlock()
		if recentlyBusy {
			time.Sleep(10 * time.Millisecond)
			children = childPIDs(shellPid)
		}
	}

	status := AgentStatus{
		ChildPIDs: children,
	}
	if status.ChildPIDs == nil {
		status.ChildPIDs = []int{}
	}

	if len(children) == 0 {
		// No children — shell is at prompt
		status.Idle = true
		status.CurrentCommand = processName(shellPid)
	} else {
		foregroundPid := children[len(children)-1] // last child is typically foreground
		status.CurrentCommand = processName(foregroundPid)

		grandchildren := childPIDs(foregroundPid)
		// Heuristic: if the shell's only child has no grandchildren and
		// shares the shell's name, it's likely a job-control self-fork
		// (e.g., bash forks itself). This can false-positive if the user
		// runs a command matching the shell name (e.g., "bash -c '...'").
		if len(grandchildren) == 0 && len(children) == 1 {
			shellName := processName(shellPid)
			childName := processName(children[0])
			if shellName == childName {
				status.Idle = true
				status.CurrentCommand = shellName
			}
		}

		// If still busy, recheck once after a brief delay to filter
		// transient children from PROMPT_COMMAND or PS1 evaluation.
		// These processes live <20ms and shouldn't count as "busy".
		if !status.Idle {
			time.Sleep(25 * time.Millisecond)
			recheck := childPIDs(shellPid)
			if len(recheck) == 0 {
				status.Idle = true
				status.ChildPIDs = []int{}
				status.CurrentCommand = processName(shellPid)
			}
		}
	}

	// Populate idle_since from tracked state
	if status.Idle {
		p.idleMu.Lock()
		if p.lastBusySeen.IsZero() {
			// Never seen busy — idle since pane creation
			status.IdleSince = p.createdAt
		} else if p.idleSince.IsZero() || p.idleSince.Before(p.lastBusySeen) {
			// First time we see idle after being busy
			p.idleSince = time.Now()
			status.IdleSince = p.idleSince
		} else {
			status.IdleSince = p.idleSince
		}
		p.idleMu.Unlock()
	} else {
		p.idleMu.Lock()
		p.lastBusySeen = time.Now()
		p.idleSince = time.Time{} // reset
		p.idleMu.Unlock()
	}

	return status
}

// childPIDs returns the PIDs of direct children of the given process.
func childPIDs(pid int) []int {
	ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if p, err := strconv.Atoi(line); err == nil {
			pids = append(pids, p)
		}
	}
	return pids
}

// processName returns the short command name for a PID.
func processName(pid int) string {
	ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(out))
	// ps may return full path on some systems; extract basename
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	// Strip leading "-" from login shells (e.g., "-bash" → "bash")
	name = strings.TrimPrefix(name, "-")
	return name
}
