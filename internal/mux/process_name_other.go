//go:build !darwin && !linux

package mux

import (
	"strconv"
)

func processName(pid int) string {
	out, err := processCommandOutput("ps", "-o", "comm=", "-p", strconv.Itoa(pid))
	if err != nil {
		return ""
	}
	return normalizeProcessName(string(out))
}
