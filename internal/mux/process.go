package mux

import (
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// AgentStatus holds the process-level status of a pane for JSON capture.
type AgentStatus struct {
	Idle           bool
	IdleSince      time.Time
	CurrentCommand string
	ChildPIDs      []int
}

// AgentStatus inspects the pane's process tree and returns its current status.
// Uses pgrep/ps for portable macOS+Linux support.
func (p *Pane) AgentStatus() AgentStatus {
	shellPid := p.ProcessPid()
	if shellPid == 0 {
		return AgentStatus{Idle: true}
	}

	children := childPIDs(shellPid)
	status := AgentStatus{
		ChildPIDs: children,
	}

	if len(children) == 0 {
		// No children — shell is at prompt
		status.Idle = true
		status.CurrentCommand = processName(shellPid)
	} else {
		// Check if the child itself has children (grandchildren of shell).
		// A single child with no grandchildren might be shell job control
		// (e.g., bash forks itself), but typically means a foreground command.
		foregroundPid := children[len(children)-1] // last child is typically foreground
		status.CurrentCommand = processName(foregroundPid)

		grandchildren := childPIDs(foregroundPid)
		// idle = child has no grandchildren AND the child's name matches the shell
		// (some shells fork themselves for job control)
		if len(grandchildren) == 0 && len(children) == 1 {
			shellName := processName(shellPid)
			childName := processName(children[0])
			if shellName == childName {
				status.Idle = true
				status.CurrentCommand = shellName
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
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
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
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
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
