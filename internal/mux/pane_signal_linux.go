//go:build linux

package mux

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func (p *Pane) notifyResizeSignal() {
	_ = p.SignalForegroundProcessGroup(syscall.SIGWINCH)
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

func (p *Pane) foregroundProcessGroup() (int, error) {
	if p.ptmx == nil {
		return 0, nil
	}

	ttyPath, err := p.ttyPath()
	if err != nil {
		return 0, err
	}
	tty, err := os.OpenFile(ttyPath, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return 0, err
	}
	defer tty.Close()

	pgrp, err := unix.IoctlGetInt(int(tty.Fd()), unix.TIOCGPGRP)
	if err != nil {
		return 0, err
	}
	return pgrp, nil
}

func (p *Pane) ttyPath() (string, error) {
	if p.ptmx == nil {
		return "", nil
	}
	ttyNum, err := unix.IoctlGetInt(int(p.ptmx.Fd()), unix.TIOCGPTN)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("/dev/pts/%d", ttyNum), nil
}
