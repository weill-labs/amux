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
// holding any session-level locks.
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
		lastBusySeen := loadUnixTime(&p.lastBusySeenUnix)
		recentlyBusy := !lastBusySeen.IsZero() && time.Since(lastBusySeen) < 500*time.Millisecond
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
		lastBusySeen := loadUnixTime(&p.lastBusySeenUnix)
		idleSince := loadUnixTime(&p.idleSinceUnix)
		if lastBusySeen.IsZero() {
			// Never seen busy — idle since pane creation
			status.IdleSince = p.createdAt
		} else if idleSince.IsZero() || idleSince.Before(lastBusySeen) {
			// First time we see idle after being busy
			now := time.Now()
			storeUnixTime(&p.idleSinceUnix, now)
			status.IdleSince = now
		} else {
			status.IdleSince = idleSince
		}
	} else {
		storeUnixTime(&p.lastBusySeenUnix, time.Now())
		storeUnixTime(&p.idleSinceUnix, time.Time{}) // reset
	}

	return status
}

func loadUnixTime(v interface{ Load() int64 }) time.Time {
	nano := v.Load()
	if nano == 0 {
		return time.Time{}
	}
	return time.Unix(0, nano)
}

func storeUnixTime(v interface{ Store(int64) }, ts time.Time) {
	if ts.IsZero() {
		v.Store(0)
		return
	}
	v.Store(ts.UnixNano())
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
