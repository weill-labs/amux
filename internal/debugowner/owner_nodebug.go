//go:build !debug

package debugowner

// Checker is a no-op in non-debug builds.
type Checker struct{}

func (c *Checker) Assert(typeName, method string) {}
