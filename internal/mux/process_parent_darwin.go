//go:build darwin

package mux

import "golang.org/x/sys/unix"

func processParentID(pid int) int {
	if pid <= 0 {
		return 0
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || info == nil {
		return 0
	}
	return int(info.Eproc.Ppid)
}
