//go:build darwin

package mux

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

const (
	iocParamShift = 13
	iocParamMask  = (1 << iocParamShift) - 1
)

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

	var pgrp int32
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		tty.Fd(),
		uintptr(syscall.TIOCGPGRP),
		uintptr(unsafe.Pointer(&pgrp)),
	)
	if errno != 0 {
		return 0, errno
	}
	return int(pgrp), nil
}

func (p *Pane) ttyPath() (string, error) {
	buf := make([]byte, (uintptr(syscall.TIOCPTYGNAME)>>16)&iocParamMask)
	if len(buf) == 0 {
		return "", errors.New("tty path buffer is empty")
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		p.ptmx.Fd(),
		uintptr(syscall.TIOCPTYGNAME),
		uintptr(unsafe.Pointer(&buf[0])),
	)
	if errno != 0 {
		return "", errno
	}

	for i, b := range buf {
		if b == 0 {
			return string(buf[:i]), nil
		}
	}
	return "", errors.New("tty path is not NUL-terminated")
}
