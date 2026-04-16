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

// processCommandOutput runs short-lived process inspection commands with both a
// context timeout and WaitDelay so inherited stdout pipes cannot wedge waits.
func processCommandOutput(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = processTimeout
	return cmd.Output()
}

// ForegroundJobState holds the pane's foreground-job state. Busy/exited waits
// and exited events use this cheap PTY-driven view rather than shell child
// enumeration.
type ForegroundJobState struct {
	Idle                   bool
	IdleSince              time.Time
	ForegroundProcessGroup int
}

// AgentStatus holds the pane's process-level status for JSON capture.
type AgentStatus struct {
	Idle           bool
	IdleSince      time.Time
	CurrentCommand string
}

func shellOnlyChildChainWithLookups(shellName string, children []int, nameForPID func(int) string, childPIDsForPID func(int) []int) bool {
	if shellName == "" || len(children) != 1 {
		return false
	}

	const maxShellChainDepth = 8
	pid := children[0]
	for depth := 0; depth < maxShellChainDepth; depth++ {
		if nameForPID(pid) != shellName {
			return false
		}
		next := childPIDsForPID(pid)
		if len(next) == 0 {
			return true
		}
		if len(next) != 1 {
			return false
		}
		pid = next[0]
	}
	return false
}

func shellOnlyForegroundChain(shellPID int, shellName string, foregroundPID int) bool {
	return shellOnlyForegroundChainWithLookups(shellPID, shellName, foregroundPID, processName, processParentID)
}

func shellOnlyForegroundChainWithLookups(shellPID int, shellName string, foregroundPID int, nameForPID func(int) string, parentPIDForPID func(int) int) bool {
	if shellPID <= 0 || foregroundPID <= 0 || shellName == "" {
		return false
	}

	const maxShellChainDepth = 8
	pid := foregroundPID
	for depth := 0; depth < maxShellChainDepth; depth++ {
		if nameForPID(pid) != shellName {
			return false
		}
		if pid == shellPID {
			return true
		}
		parent := parentPIDForPID(pid)
		if parent <= 0 || parent == pid {
			return false
		}
		pid = parent
	}
	return false
}

// ForegroundJobState reports whether the shell currently owns the terminal
// foreground process group. Safe to call without holding any session-level
// locks.
func (p *Pane) ForegroundJobState() ForegroundJobState {
	shellPid := p.ProcessPid()
	if shellPid == 0 {
		return ForegroundJobState{
			Idle:      true,
			IdleSince: p.createdAt,
		}
	}

	shellPgrp := processGroupID(shellPid)
	if shellPgrp == 0 {
		shellPgrp = shellPid
	}

	foregroundPgrp, err := p.foregroundProcessGroup()
	if err != nil || foregroundPgrp <= 0 {
		foregroundPgrp = shellPgrp
	}

	state := ForegroundJobState{
		ForegroundProcessGroup: foregroundPgrp,
	}
	markIdle := func() {
		state.Idle = true
		state.ForegroundProcessGroup = 0
	}
	if foregroundPgrp == shellPgrp || shellOnlyForegroundChain(shellPid, p.ShellName(), foregroundPgrp) {
		markIdle()
	}

	if state.Idle {
		lastBusySeen := loadUnixTime(&p.lastBusySeenUnix)
		idleSince := loadUnixTime(&p.idleSinceUnix)
		if lastBusySeen.IsZero() {
			state.IdleSince = p.createdAt
		} else if idleSince.IsZero() || idleSince.Before(lastBusySeen) {
			now := time.Now()
			storeUnixTime(&p.idleSinceUnix, now)
			state.IdleSince = now
		} else {
			state.IdleSince = idleSince
		}
	} else {
		storeUnixTime(&p.lastBusySeenUnix, time.Now())
		storeUnixTime(&p.idleSinceUnix, time.Time{})
	}

	return state
}

// AgentStatus reports process-level status for capture/debugging. When idle,
// CurrentCommand reports the shell name. When busy, it reports the foreground
// process-group leader name when available.
func (p *Pane) AgentStatus() AgentStatus {
	state := p.ForegroundJobState()
	status := AgentStatus{
		Idle:      state.Idle,
		IdleSince: state.IdleSince,
	}
	if state.Idle {
		status.CurrentCommand = p.ShellName()
		return status
	}

	status.CurrentCommand = processName(state.ForegroundProcessGroup)
	if status.CurrentCommand == "" {
		status.CurrentCommand = p.ShellName()
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
	out, err := processCommandOutput("pgrep", "-P", strconv.Itoa(pid))
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
