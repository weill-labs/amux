//go:build debug

package debugowner

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

// Checker records the first mutating goroutine and panics on later mutations
// from a different goroutine in debug builds.
type Checker struct {
	owner atomic.Uint64
}

func (c *Checker) Assert(typeName, method string) {
	gid := currentGoroutineID()
	if gid == 0 {
		return
	}
	if owner := c.owner.Load(); owner == gid {
		return
	}
	if c.owner.CompareAndSwap(0, gid) {
		return
	}
	owner := c.owner.Load()
	if owner != gid {
		panic(fmt.Sprintf("%s.%s called from goroutine %d; owner is goroutine %d", typeName, method, gid, owner))
	}
}

func currentGoroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	line := strings.TrimPrefix(string(buf[:n]), "goroutine ")
	field := line
	if i := strings.IndexByte(line, ' '); i >= 0 {
		field = line[:i]
	}
	gid, _ := strconv.ParseUint(field, 10, 64)
	return gid
}
