//go:build darwin || linux

package mux

import "golang.org/x/sys/unix"

func processGroupID(pid int) int {
	if pid <= 0 {
		return 0
	}
	pgid, err := unix.Getpgid(pid)
	if err != nil {
		return 0
	}
	return pgid
}
