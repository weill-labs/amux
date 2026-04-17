package client

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/transport"
)

func TestDefaultSSHRunSessionOps(t *testing.T) {
	t.Parallel()

	ops := defaultSSHRunSessionOps()
	if ops.newTransport == nil || ops.deployBinary == nil || ops.ensureRemoteServer == nil || ops.dialRemoteSocket == nil {
		t.Fatalf("defaultSSHRunSessionOps() = %#v, want all hooks wired", ops)
	}
}

func TestRunSSHSessionReturnsConfigLoadError(t *testing.T) {
	configPath := t.TempDir() + "/config.toml"
	if err := os.WriteFile(configPath, []byte("["), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("AMUX_CONFIG", configPath)

	err := RunSSHSession(transport.Target{
		User:    "deploy",
		Host:    "builder",
		Port:    "22",
		Session: "work",
	})
	if err == nil {
		t.Fatal("RunSSHSession() error = nil, want config load failure")
	}
	if !strings.Contains(err.Error(), "loading config") {
		t.Fatalf("RunSSHSession() error = %q, want loading config", err.Error())
	}
}

func TestRunSSHSessionUsesRunner(t *testing.T) {
	configPath := t.TempDir() + "/config.toml"
	if err := os.WriteFile(configPath, []byte(`
[hosts.builder]
transport = "ssh"
address = "10.0.0.5:2222"
identity_file = "/tmp/id_builder"
`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("AMUX_CONFIG", configPath)

	getSize := func(int) (int, int, error) { return 80, 24, nil }
	ops := sshRunSessionOps{
		newTransport: func(string, config.Host) (transport.Transport, error) {
			return &stubSessionTransport{}, nil
		},
		deployBinary:       func(transport.Transport, transport.Target, string) error { return nil },
		ensureRemoteServer: func(transport.Transport, transport.Target, string) error { return nil },
		dialRemoteSocket:   func(transport.Transport, transport.Target) (net.Conn, error) { return noopConn{}, nil },
	}

	called := false
	err := runSSHSession(transport.Target{
		User:    "deploy",
		Host:    "builder",
		Port:    "22",
		Session: "work",
	}, getSize, ops, func(sessionName string, gotGetSize func(int) (int, int, error), deps runSessionDeps) error {
		called = true
		if sessionName != "work" {
			t.Fatalf("runner sessionName = %q, want work", sessionName)
		}
		if gotGetSize == nil {
			t.Fatal("runner getTermSize should be wired")
		}
		if deps.ensureDaemon == nil || deps.dial == nil {
			t.Fatal("runner deps should include SSH ensureDaemon and dial hooks")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("runSSHSession() error = %v", err)
	}
	if !called {
		t.Fatal("runner was not called")
	}
}

func TestResolveSSHSessionTargetUsesConfiguredTransportAndHostConfig(t *testing.T) {
	configPath := t.TempDir() + "/config.toml"
	if err := os.WriteFile(configPath, []byte(`
[hosts.builder]
transport = "ssh"
address = "10.0.0.5:2222"
identity_file = "/tmp/id_builder"
`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("AMUX_CONFIG", configPath)

	target, err := resolveSSHSessionTarget(transport.Target{
		User:    "deploy",
		Host:    "builder",
		Port:    "22",
		Session: "work",
	})
	if err != nil {
		t.Fatalf("resolveSSHSessionTarget() error = %v", err)
	}
	if target.Transport != "ssh" {
		t.Fatalf("resolveSSHSessionTarget() transport = %q, want ssh", target.Transport)
	}
	if target.HostConfig.Address != "10.0.0.5:2222" {
		t.Fatalf("resolveSSHSessionTarget() address = %q, want %q", target.HostConfig.Address, "10.0.0.5:2222")
	}
	if target.HostConfig.IdentityFile != "/tmp/id_builder" {
		t.Fatalf("resolveSSHSessionTarget() identity file = %q, want %q", target.HostConfig.IdentityFile, "/tmp/id_builder")
	}
}

func TestSSHRunSessionDepsEnsureDaemonAndDial(t *testing.T) {
	t.Parallel()

	state := &sshSessionState{}
	probeConn := noopConn{}
	wantConn := noopConn{}
	tr := &stubSessionTransport{name: "ssh"}

	var calls []string
	deps := sshRunSessionDeps(sshSessionTarget{
		Target: transport.Target{
			User:    "alice",
			Host:    "builder",
			Port:    "22",
			Session: "work",
		},
		Transport: "ssh",
		HostConfig: config.Host{
			Address:      "10.0.0.5:2222",
			IdentityFile: "/tmp/id_builder",
		},
	}, state, sshRunSessionOps{
		newTransport: func(name string, hostCfg config.Host) (transport.Transport, error) {
			calls = append(calls, "new-transport")
			if name != "ssh" {
				t.Fatalf("newTransport name = %q, want ssh", name)
			}
			if hostCfg.Address != "10.0.0.5:2222" {
				t.Fatalf("newTransport address = %q, want 10.0.0.5:2222", hostCfg.Address)
			}
			if hostCfg.IdentityFile != "/tmp/id_builder" {
				t.Fatalf("newTransport identityFile = %q, want /tmp/id_builder", hostCfg.IdentityFile)
			}
			return tr, nil
		},
		deployBinary: func(got transport.Transport, target transport.Target, buildHash string) error {
			calls = append(calls, "deploy")
			if got != tr {
				t.Fatal("deployBinary received unexpected transport")
			}
			if target.Host != "builder" || target.Session != "work" {
				t.Fatalf("deployBinary target = %#v, want builder/work target", target)
			}
			if buildHash == "" {
				t.Fatal("deployBinary buildHash should not be empty")
			}
			return nil
		},
		ensureRemoteServer: func(got transport.Transport, target transport.Target, sessionName string) error {
			calls = append(calls, "ensure-remote")
			if got != tr {
				t.Fatal("ensureRemoteServer received unexpected transport")
			}
			if target.Host != "builder" || target.Session != "work" {
				t.Fatalf("ensureRemoteServer target = %#v, want builder/work target", target)
			}
			if sessionName != "work" {
				t.Fatalf("ensureRemoteServer sessionName = %q, want work", sessionName)
			}
			return nil
		},
		dialRemoteSocket: func(got transport.Transport, target transport.Target) (net.Conn, error) {
			if got != tr {
				t.Fatal("dialRemoteSocket received unexpected transport")
			}
			if target.Host != "builder" || target.Session != "work" {
				t.Fatalf("dialRemoteSocket target = %#v, want builder/work target", target)
			}
			if len(calls) == 3 {
				calls = append(calls, "probe-dial")
				return probeConn, nil
			}
			calls = append(calls, "session-dial")
			return wantConn, nil
		},
	})

	if err := deps.ensureDaemon("work", 200*time.Millisecond); err != nil {
		t.Fatalf("ensureDaemon() error = %v", err)
	}
	conn, err := deps.dial("unix", "/tmp/ignored")
	if err != nil {
		t.Fatalf("dial() error = %v", err)
	}
	if conn != wantConn {
		t.Fatalf("dial() = %#v, want %#v", conn, wantConn)
	}

	wantCalls := []string{
		"new-transport",
		"deploy",
		"ensure-remote",
		"probe-dial",
		"session-dial",
	}
	if len(calls) != len(wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	for i := range wantCalls {
		if calls[i] != wantCalls[i] {
			t.Fatalf("calls[%d] = %q, want %q", i, calls[i], wantCalls[i])
		}
	}
}

func TestSSHRunSessionDepsIgnoresDeployFailure(t *testing.T) {
	t.Parallel()

	state := &sshSessionState{}
	tr := &stubSessionTransport{name: "ssh"}

	deps := sshRunSessionDeps(sshSessionTarget{
		Target: transport.Target{
			User:    "alice",
			Host:    "builder",
			Port:    "22",
			Session: "main",
		},
		Transport: "ssh",
	}, state, sshRunSessionOps{
		newTransport:       func(string, config.Host) (transport.Transport, error) { return tr, nil },
		deployBinary:       func(transport.Transport, transport.Target, string) error { return errors.New("deploy failed") },
		ensureRemoteServer: func(transport.Transport, transport.Target, string) error { return nil },
		dialRemoteSocket:   func(transport.Transport, transport.Target) (net.Conn, error) { return noopConn{}, nil },
	})

	if err := deps.ensureDaemon("main", 200*time.Millisecond); err != nil {
		t.Fatalf("ensureDaemon() error = %v, want deploy failure to be ignored", err)
	}
}

type stubSessionTransport struct {
	name string
}

func (s *stubSessionTransport) Name() string {
	return s.name
}

func (s *stubSessionTransport) Dial(context.Context, transport.Target) (net.Conn, error) {
	return noopConn{}, nil
}

func (s *stubSessionTransport) Deploy(context.Context, transport.Target, string) error {
	return nil
}

func (s *stubSessionTransport) EnsureServer(context.Context, transport.Target, string) error {
	return nil
}

func (s *stubSessionTransport) Close() error {
	return nil
}

type noopConn struct{}

func (noopConn) Read([]byte) (int, error)         { return 0, nil }
func (noopConn) Write([]byte) (int, error)        { return 0, nil }
func (noopConn) Close() error                     { return nil }
func (noopConn) LocalAddr() net.Addr              { return nil }
func (noopConn) RemoteAddr() net.Addr             { return nil }
func (noopConn) SetDeadline(time.Time) error      { return nil }
func (noopConn) SetReadDeadline(time.Time) error  { return nil }
func (noopConn) SetWriteDeadline(time.Time) error { return nil }
