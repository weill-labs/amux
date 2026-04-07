//go:build darwin

package mux

import "golang.org/x/sys/unix"

func processName(pid int) string {
	if pid <= 0 {
		return ""
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || info == nil {
		return ""
	}
	return normalizeProcessName(unix.ByteSliceToString(info.Proc.P_comm[:]))
}
