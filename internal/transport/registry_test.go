package transport

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/weill-labs/amux/internal/config"
)

var registryTestMu sync.Mutex

func TestRegisterAndGet(t *testing.T) {
	t.Parallel()
	registryTestMu.Lock()
	t.Cleanup(registryTestMu.Unlock)
	restoreRegistry(t)

	name := strings.ToLower(t.Name())
	want := &stubTransport{name: name}

	Register(name, func(config.Host) (Transport, error) {
		return want, nil
	})

	got, err := Get(name, config.Host{})
	if err != nil {
		t.Fatalf("Get(%q) error = %v", name, err)
	}
	if got != want {
		t.Fatalf("Get(%q) = %#v, want %#v", name, got, want)
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	t.Parallel()
	registryTestMu.Lock()
	t.Cleanup(registryTestMu.Unlock)
	restoreRegistry(t)

	name := strings.ToLower(t.Name())
	Register(name, func(config.Host) (Transport, error) {
		return &stubTransport{name: name}, nil
	})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Register() duplicate = nil panic, want duplicate registration panic")
		}
	}()
	Register(name, func(config.Host) (Transport, error) {
		return &stubTransport{name: name + "-dup"}, nil
	})
}

func TestGetUnknownTransport(t *testing.T) {
	t.Parallel()
	registryTestMu.Lock()
	t.Cleanup(registryTestMu.Unlock)
	restoreRegistry(t)

	name := strings.ToLower(t.Name())
	_, err := Get(name, config.Host{})
	if err == nil {
		t.Fatalf("Get(%q) error = nil, want unknown transport error", name)
	}
	if !strings.Contains(err.Error(), name) {
		t.Fatalf("Get(%q) error = %q, want unknown transport name", name, err.Error())
	}
}

func TestNamesIncludesRegisteredTransport(t *testing.T) {
	t.Parallel()
	registryTestMu.Lock()
	t.Cleanup(registryTestMu.Unlock)
	restoreRegistry(t)

	name := strings.ToLower(t.Name())
	Register(name, func(config.Host) (Transport, error) {
		return &stubTransport{name: name}, nil
	})

	names := Names()
	for _, got := range names {
		if got == name {
			return
		}
	}
	t.Fatalf("Names() = %v, want %q", names, name)
}

type stubTransport struct {
	name string
}

func (s *stubTransport) Name() string {
	return s.name
}

func (s *stubTransport) Dial(context.Context, Target) (net.Conn, error) {
	return nil, nil
}

func (s *stubTransport) Deploy(context.Context, Target, string) error {
	return nil
}

func (s *stubTransport) EnsureServer(context.Context, Target, string) error {
	return nil
}

func (s *stubTransport) Close() error {
	return nil
}

func restoreRegistry(t *testing.T) {
	t.Helper()

	registryMu.Lock()
	snapshot := make(map[string]Factory, len(registry))
	for name, factory := range registry {
		snapshot[name] = factory
	}
	registryMu.Unlock()

	t.Cleanup(func() {
		registryMu.Lock()
		registry = make(map[string]Factory, len(snapshot))
		for name, factory := range snapshot {
			registry[name] = factory
		}
		registryMu.Unlock()
	})
}
