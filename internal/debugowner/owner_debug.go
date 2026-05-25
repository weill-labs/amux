//go:build debug

package debugowner

import (
	"fmt"
	"sync/atomic"

	"github.com/weill-labs/amux/internal/goroutineid"
)

// Checker records the first mutating goroutine and panics on later mutations
// from a different goroutine in debug builds.
type Checker struct {
	owner atomic.Uint64
}

func (c *Checker) Assert(typeName, method string) {
	gid := goroutineid.Current()
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

// PanicIfCurrent panics when the current goroutine matches the recorded owner.
func (c *Checker) PanicIfCurrent(typeName, method string) {
	gid := goroutineid.Current()
	if gid == 0 {
		return
	}
	if owner := c.owner.Load(); owner != 0 && owner == gid {
		panic(fmt.Sprintf("%s.%s called from owner goroutine %d", typeName, method, gid))
	}
}
