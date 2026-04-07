//go:build !darwin && !linux

package mux

func processGroupID(pid int) int {
	return pid
}
