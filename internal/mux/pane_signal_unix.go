//go:build darwin || linux

package mux

import "syscall"

// SignalForegroundProcessGroup sends sig to the pane's foreground job, falling
// back to the shell process when no foreground process group is available.
func (p *Pane) SignalForegroundProcessGroup(sig syscall.Signal) error {
	pgrp, err := p.foregroundProcessGroup()
	if err == nil && pgrp > 0 {
		return syscall.Kill(-pgrp, sig)
	}
	return p.SignalProcess(sig)
}

// SignalProcess sends sig directly to the pane's shell process.
func (p *Pane) SignalProcess(sig syscall.Signal) error {
	pid := p.ProcessPid()
	if pid == 0 {
		return nil
	}
	return syscall.Kill(pid, sig)
}
