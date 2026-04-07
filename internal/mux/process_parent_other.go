//go:build !darwin && !linux

package mux

func processParentID(pid int) int {
	return 0
}
