package transport

import (
	"context"
	"errors"
	"net"
	"reflect"
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

func TestGetAutoFallsBackAndCachesSelection(t *testing.T) {
	t.Parallel()
	registryTestMu.Lock()
	t.Cleanup(registryTestMu.Unlock)
	restoreRegistry(t)

	var calls []string
	factoryCalls := map[string]int{}

	Register("mosh", func(config.Host) (Transport, error) {
		factoryCalls["mosh"]++
		return &stubTransport{
			name: "mosh",
			deployFunc: func(context.Context, Target, string) error {
				calls = append(calls, "mosh deploy")
				return errors.New("mosh transport not yet implemented")
			},
			dialFunc: func(context.Context, Target) (net.Conn, error) {
				calls = append(calls, "mosh dial")
				return nil, errors.New("mosh transport not yet implemented")
			},
			closeFunc: func() error {
				calls = append(calls, "mosh close")
				return nil
			},
		}, nil
	})
	Register("ssh", func(config.Host) (Transport, error) {
		factoryCalls["ssh"]++
		return &stubTransport{
			name: "ssh",
			deployFunc: func(context.Context, Target, string) error {
				calls = append(calls, "ssh deploy")
				return nil
			},
			dialFunc: func(context.Context, Target) (net.Conn, error) {
				calls = append(calls, "ssh dial")
				clientConn, serverConn := net.Pipe()
				_ = serverConn.Close()
				return clientConn, nil
			},
			closeFunc: func() error {
				calls = append(calls, "ssh close")
				return nil
			},
		}, nil
	})

	tr, err := GetAuto(config.Host{}, []string{"mosh", "ssh"})
	if err != nil {
		t.Fatalf("GetAuto() error = %v", err)
	}
	if got := tr.Name(); got != "auto" {
		t.Fatalf("Name() before selection = %q, want auto", got)
	}
	if err := tr.Deploy(context.Background(), Target{Host: "builder"}, "abc1234"); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	if got := tr.Name(); got != "ssh" {
		t.Fatalf("Name() after fallback = %q, want ssh", got)
	}

	conn, err := tr.Dial(context.Background(), Target{Host: "builder", Session: "main"})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	_ = conn.Close()
	conn, err = tr.Dial(context.Background(), Target{Host: "builder", Session: "main"})
	if err != nil {
		t.Fatalf("Dial() second call error = %v", err)
	}
	_ = conn.Close()
	if err := tr.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	wantCalls := []string{
		"mosh deploy",
		"mosh close",
		"ssh deploy",
		"ssh dial",
		"ssh dial",
		"ssh close",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	if factoryCalls["mosh"] != 1 || factoryCalls["ssh"] != 1 {
		t.Fatalf("factoryCalls = %v, want one instance per transport", factoryCalls)
	}
}

func TestGetAutoUsesDefaultPreferencesWhenUnset(t *testing.T) {
	t.Parallel()
	registryTestMu.Lock()
	t.Cleanup(registryTestMu.Unlock)
	restoreRegistry(t)

	Register("mosh", func(config.Host) (Transport, error) {
		return &stubTransport{
			name: "mosh",
			deployFunc: func(context.Context, Target, string) error {
				return errors.New("mosh transport not yet implemented")
			},
		}, nil
	})
	Register("ssh", func(config.Host) (Transport, error) {
		return &stubTransport{
			name:       "ssh",
			deployFunc: func(context.Context, Target, string) error { return nil },
		}, nil
	})

	tr, err := Get("auto", config.Host{})
	if err != nil {
		t.Fatalf("Get(%q) error = %v", "auto", err)
	}
	if err := tr.Deploy(context.Background(), Target{Host: "builder"}, "hash"); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	if got := tr.Name(); got != "ssh" {
		t.Fatalf("Name() after default fallback = %q, want ssh", got)
	}
}

type stubTransport struct {
	name       string
	dialFunc   func(context.Context, Target) (net.Conn, error)
	deployFunc func(context.Context, Target, string) error
	ensureFunc func(context.Context, Target, string) error
	closeFunc  func() error
}

func (s *stubTransport) Name() string {
	return s.name
}

func (s *stubTransport) Dial(ctx context.Context, target Target) (net.Conn, error) {
	if s.dialFunc != nil {
		return s.dialFunc(ctx, target)
	}
	return nil, nil
}

func (s *stubTransport) Deploy(ctx context.Context, target Target, buildHash string) error {
	if s.deployFunc != nil {
		return s.deployFunc(ctx, target, buildHash)
	}
	return nil
}

func (s *stubTransport) EnsureServer(ctx context.Context, target Target, session string) error {
	if s.ensureFunc != nil {
		return s.ensureFunc(ctx, target, session)
	}
	return nil
}

func (s *stubTransport) Close() error {
	if s.closeFunc != nil {
		return s.closeFunc()
	}
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
