//go:build darwin

package mux

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
)

// PaneCwd returns the current working directory of a process by PID.
// On macOS, uses lsof since /proc doesn't exist. Returns "" on error
// (cwd is best-effort — the process may have exited or lsof may not be available).
func PaneCwd(pid int) string {
	if pid <= 0 {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
	defer cancel()
	// lsof -a -p PID -d cwd -Fn outputs:
	//   p<PID>
	//   fcwd
	//   n/path/to/cwd
	out, err := exec.CommandContext(ctx, "lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n/") {
			return line[1:]
		}
	}
	return ""
}
