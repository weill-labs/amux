package transport

import (
	"context"
	"fmt"
	"net"
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
	if name == "auto" {
		return GetAuto(cfg, cfg.TransportPreference)
	}
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

func GetAuto(cfg config.Host, preferences []string) (Transport, error) {
	prefs := preferences
	if len(prefs) == 0 {
		prefs = config.DefaultTransportPreferences()
	}
	normalized := make([]string, 0, len(prefs))
	seen := make(map[string]struct{}, len(prefs))
	for _, name := range prefs {
		name = strings.TrimSpace(name)
		if name == "" || name == "auto" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		normalized = append(normalized, name)
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("auto transport has no candidate transports")
	}
	return &autoTransport{cfg: cfg, preferences: normalized}, nil
}

type autoTransport struct {
	cfg         config.Host
	preferences []string

	mu       sync.Mutex
	selected Transport
}

func (t *autoTransport) Name() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.selected != nil {
		return t.selected.Name()
	}
	return "auto"
}

func (t *autoTransport) Dial(ctx context.Context, target Target) (net.Conn, error) {
	t.mu.Lock()
	selected := t.selected
	t.mu.Unlock()
	if selected != nil {
		return selected.Dial(ctx, target)
	}

	var errs []string
	for _, name := range t.preferences {
		tr, err := getTransportInstance(name, t.cfg)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		conn, err := tr.Dial(ctx, target)
		if err == nil {
			t.mu.Lock()
			t.selected = tr
			t.mu.Unlock()
			return conn, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", name, err))
		_ = tr.Close()
	}
	return nil, fmt.Errorf("auto transport failed: %s", strings.Join(errs, "; "))
}

func (t *autoTransport) Deploy(ctx context.Context, target Target, buildHash string) error {
	return t.try(func(tr Transport) error {
		return tr.Deploy(ctx, target, buildHash)
	})
}

func (t *autoTransport) EnsureServer(ctx context.Context, target Target, session string) error {
	return t.try(func(tr Transport) error {
		return tr.EnsureServer(ctx, target, session)
	})
}

func (t *autoTransport) Close() error {
	t.mu.Lock()
	selected := t.selected
	t.selected = nil
	t.mu.Unlock()
	if selected == nil {
		return nil
	}
	return selected.Close()
}

func (t *autoTransport) try(op func(Transport) error) error {
	t.mu.Lock()
	selected := t.selected
	t.mu.Unlock()
	if selected != nil {
		return op(selected)
	}

	var errs []string
	for _, name := range t.preferences {
		tr, err := getTransportInstance(name, t.cfg)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		if err := op(tr); err == nil {
			t.mu.Lock()
			t.selected = tr
			t.mu.Unlock()
			return nil
		} else {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
		}
		_ = tr.Close()
	}
	return fmt.Errorf("auto transport failed: %s", strings.Join(errs, "; "))
}

func getTransportInstance(name string, cfg config.Host) (Transport, error) {
	registryMu.RLock()
	factory, ok := registry[name]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown transport %q (available: %s)", name, strings.Join(Names(), ", "))
	}
	return factory(cfg)
}
