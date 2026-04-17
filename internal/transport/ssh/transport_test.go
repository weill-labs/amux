package ssh

import (
	"context"
	"net"
	"testing"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/transport"
	gossh "golang.org/x/crypto/ssh"
)

func TestRegisteredSSHTransport(t *testing.T) {
	t.Parallel()

	tr, err := transport.Get("ssh", config.Host{})
	if err != nil {
		t.Fatalf("Get(%q) error = %v", "ssh", err)
	}
	if got := tr.Name(); got != "ssh" {
		t.Fatalf("Name() = %q, want ssh", got)
	}
}

func TestSSHTransportEnsureServerUsesRemoteSocketPath(t *testing.T) {
	t.Parallel()

	var calls []string
	client := new(gossh.Client)
	tr := newSSHTransportWithDeps(config.Host{
		Address:      "10.0.0.5",
		IdentityFile: "/tmp/id_builder",
	}, sshTransportDeps{
		buildSSHConfig: func(user, identityFile string) (*gossh.ClientConfig, error) {
			calls = append(calls, "build-config")
			if user != "deploy" {
				t.Fatalf("buildSSHConfig user = %q, want deploy", user)
			}
			if identityFile != "/tmp/id_builder" {
				t.Fatalf("buildSSHConfig identityFile = %q, want /tmp/id_builder", identityFile)
			}
			return &gossh.ClientConfig{}, nil
		},
		sshDial: func(network, addr string, cfg *gossh.ClientConfig) (*gossh.Client, error) {
			calls = append(calls, "ssh-dial")
			if network != "tcp" {
				t.Fatalf("sshDial network = %q, want tcp", network)
			}
			if addr != "10.0.0.5:2200" {
				t.Fatalf("sshDial addr = %q, want 10.0.0.5:2200", addr)
			}
			return client, nil
		},
		sshOutput: func(got *gossh.Client, cmd string) (string, error) {
			calls = append(calls, "ssh-output")
			if got != client {
				t.Fatal("sshOutput received unexpected client")
			}
			if cmd != "id -u" {
				t.Fatalf("sshOutput cmd = %q, want id -u", cmd)
			}
			return "1001", nil
		},
		ensureRemoteServer: func(got *gossh.Client, sockPath, session string) error {
			calls = append(calls, "ensure-remote")
			if got != client {
				t.Fatal("ensureRemoteServer received unexpected client")
			}
			if sockPath != "/tmp/amux-1001/work" {
				t.Fatalf("ensureRemoteServer sockPath = %q, want /tmp/amux-1001/work", sockPath)
			}
			if session != "work" {
				t.Fatalf("ensureRemoteServer session = %q, want work", session)
			}
			return nil
		},
		remoteSocketPath: RemoteSocketPath,
		normalizeAddr:    NormalizeAddr,
	})

	if err := tr.EnsureServer(context.Background(), transport.Target{
		Host:    "builder",
		User:    "deploy",
		Port:    "2200",
		Session: "work",
	}, "work"); err != nil {
		t.Fatalf("EnsureServer() error = %v", err)
	}

	wantCalls := []string{"build-config", "ssh-dial", "ssh-output", "ensure-remote"}
	if len(calls) != len(wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	for i, want := range wantCalls {
		if calls[i] != want {
			t.Fatalf("calls[%d] = %q, want %q", i, calls[i], want)
		}
	}
}

func TestSSHTransportDialUsesCachedClient(t *testing.T) {
	t.Parallel()

	client := new(gossh.Client)
	connClient, connServer := net.Pipe()
	t.Cleanup(func() {
		_ = connClient.Close()
		_ = connServer.Close()
	})

	dialCalls := 0
	tr := newSSHTransportWithDeps(config.Host{}, sshTransportDeps{
		buildSSHConfig: func(string, string) (*gossh.ClientConfig, error) {
			return &gossh.ClientConfig{}, nil
		},
		sshDial: func(string, string, *gossh.ClientConfig) (*gossh.Client, error) {
			dialCalls++
			return client, nil
		},
		sshOutput: func(*gossh.Client, string) (string, error) {
			return "1001", nil
		},
		dialRemoteSocket: func(got *gossh.Client, sockPath string) (net.Conn, error) {
			if got != client {
				t.Fatal("dialRemoteSocket received unexpected client")
			}
			if sockPath != "/tmp/amux-1001/work" {
				t.Fatalf("dialRemoteSocket sockPath = %q, want /tmp/amux-1001/work", sockPath)
			}
			return connClient, nil
		},
		remoteSocketPath: RemoteSocketPath,
		normalizeAddr:    NormalizeAddr,
	})

	target := transport.Target{
		Host:    "builder",
		User:    "deploy",
		Port:    "22",
		Session: "work",
	}
	if _, err := tr.Dial(context.Background(), target); err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	if _, err := tr.Dial(context.Background(), target); err != nil {
		t.Fatalf("Dial() second call error = %v", err)
	}
	if dialCalls != 1 {
		t.Fatalf("sshDial calls = %d, want 1 cached dial", dialCalls)
	}
}

func TestSSHTransportDeployUsesCachedClient(t *testing.T) {
	t.Parallel()

	client := new(gossh.Client)
	tr := newSSHTransportWithDeps(config.Host{}, sshTransportDeps{
		buildSSHConfig: func(string, string) (*gossh.ClientConfig, error) {
			return &gossh.ClientConfig{}, nil
		},
		sshDial: func(string, string, *gossh.ClientConfig) (*gossh.Client, error) {
			return client, nil
		},
		deployBinary: func(got *gossh.Client, buildHash string) error {
			if got != client {
				t.Fatal("deployBinary received unexpected client")
			}
			if buildHash != "abc1234" {
				t.Fatalf("deployBinary buildHash = %q, want abc1234", buildHash)
			}
			return nil
		},
		normalizeAddr: NormalizeAddr,
	})

	if err := tr.Deploy(context.Background(), transport.Target{Host: "builder", User: "deploy", Port: "22"}, "abc1234"); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
}

func TestSSHTransportCloseClosesCachedClient(t *testing.T) {
	t.Parallel()

	client := new(gossh.Client)
	closed := 0
	tr := newSSHTransportWithDeps(config.Host{}, sshTransportDeps{
		closeClient: func(got *gossh.Client) error {
			if got != client {
				t.Fatal("closeClient received unexpected client")
			}
			closed++
			return nil
		},
	})
	tr.client = client
	tr.remoteUID = "1001"

	if err := tr.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if closed != 1 {
		t.Fatalf("closeClient calls = %d, want 1", closed)
	}
	if tr.client != nil {
		t.Fatal("Close() should clear cached client")
	}
	if tr.remoteUID != "" {
		t.Fatalf("Close() remoteUID = %q, want empty", tr.remoteUID)
	}
}

func TestSSHTransportContextCancellation(t *testing.T) {
	t.Parallel()

	tr := newSSHTransportWithDeps(config.Host{}, sshTransportDeps{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := tr.Dial(ctx, transport.Target{}); err == nil {
		t.Fatal("Dial() error = nil, want context cancellation")
	}
	if err := tr.Deploy(ctx, transport.Target{}, "hash"); err == nil {
		t.Fatal("Deploy() error = nil, want context cancellation")
	}
	if err := tr.EnsureServer(ctx, transport.Target{}, "main"); err == nil {
		t.Fatal("EnsureServer() error = nil, want context cancellation")
	}
}
