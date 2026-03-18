//go:build linux

package mux

import (
	"os"
	"strconv"
)

// PaneCwd returns the current working directory of a process by PID.
// On Linux, reads /proc/PID/cwd symlink. Returns "" on error
// (cwd is best-effort — the process may have exited or /proc may not be readable).
func PaneCwd(pid int) string {
	if pid <= 0 {
		return ""
	}
	target, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	if err != nil {
		return ""
	}
	return target
}
