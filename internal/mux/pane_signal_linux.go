//go:build linux

package mux

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func (p *Pane) foregroundProcessGroup() (int, error) {
	if p.ptmx == nil {
		return 0, nil
	}

	ttyPath, err := p.ttyPath()
	if err != nil {
		return 0, err
	}
	tty, err := os.OpenFile(ttyPath, os.O_RDWR|unix.O_NOCTTY, 0)
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
