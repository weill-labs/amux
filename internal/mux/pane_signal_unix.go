//go:build darwin || linux

package mux

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// foregroundProcessGroup returns the foreground process group of the pane's
// terminal via TIOCGPGRP on the PTY master. The master fd is required: macOS
// rejects TIOCGPGRP with ENOTTY on a freshly opened slave path (it is already
// the controlling terminal of the shell's session), so reading the master is
// the only portable approach across darwin and linux.
func (p *Pane) foregroundProcessGroup() (int, error) {
	if p.ptmx == nil {
		return 0, nil
	}

	pgrp, err := unix.IoctlGetInt(int(p.ptmx.Fd()), unix.TIOCGPGRP)
	if err != nil {
		return 0, err
	}
	return pgrp, nil
}

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
