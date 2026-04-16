//go:build linux

package mux

import (
	"golang.org/x/sys/unix"
)

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
