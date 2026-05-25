package goroutineid

import (
	"bytes"
	"runtime"
	"strconv"
)

// Current parses the current goroutine ID from runtime.Stack's first line.
// It is a debug/diagnostic aid only; production logic must not depend on a
// goroutine ID remaining stable across implementation changes in the runtime.
func Current() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	fields := bytes.Fields(buf[:n])
	if len(fields) < 2 {
		return 0
	}
	id, err := strconv.ParseUint(string(fields[1]), 10, 64)
	if err != nil {
		return 0
	}
	return id
}
