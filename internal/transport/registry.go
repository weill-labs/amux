package transport

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/weill-labs/amux/internal/config"
)

var (
	registryMu sync.RWMutex
	registry   = make(map[string]Factory)
)

// Register associates a transport name with a factory. Called from each
// transport package's init().
func Register(name string, f Factory) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		panic("transport: empty transport name")
	case f == nil:
		panic("transport: nil factory for " + name)
	}

	registryMu.Lock()
	defer registryMu.Unlock()

	if _, exists := registry[name]; exists {
		panic("transport: duplicate registration for " + name)
	}
	registry[name] = f
}

// Get returns a transport instance for the given name and config.
func Get(name string, cfg config.Host) (Transport, error) {
	registryMu.RLock()
	factory, ok := registry[name]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown transport %q (available: %s)", name, strings.Join(Names(), ", "))
	}
	return factory(cfg)
}

// Names returns all registered transport names, for help/errors.
func Names() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()

	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
