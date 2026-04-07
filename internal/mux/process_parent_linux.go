//go:build linux

package mux

import (
	"os"
	"strconv"
	"strings"
)

func processParentID(pid int) int {
	if pid <= 0 {
		return 0
	}

	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	raw := strings.TrimSpace(string(data))
	closeIdx := strings.LastIndex(raw, ")")
	if closeIdx < 0 || closeIdx+2 > len(raw) {
		return 0
	}
	fields := strings.Fields(raw[closeIdx+2:])
	if len(fields) < 2 {
		return 0
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0
	}
	return parent
}
