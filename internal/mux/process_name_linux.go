//go:build linux

package mux

import (
	"os"
	"strconv"
)

func processName(pid int) string {
	if pid <= 0 {
		return ""
	}
	out, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return ""
	}
	return normalizeProcessName(string(out))
}
