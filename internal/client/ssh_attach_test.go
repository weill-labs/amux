package client

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/weill-labs/amux/internal/sshutil"
)

func TestResolveSSHSessionTargetUsesConfiguredAddressAndIdentity(t *testing.T) {
	configPath := t.TempDir() + "/config.toml"
	if err := os.WriteFile(configPath, []byte(`
[hosts.builder]
address = "10.0.0.5:2222"
identity_file = "/tmp/id_builder"
`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("AMUX_CONFIG", configPath)

	target, err := resolveSSHSessionTarget(sshutil.SSHTarget{
		User:    "deploy",
		Host:    "builder",
		Port:    "22",
		Session: "work",
	})
	if err != nil {
		t.Fatalf("resolveSSHSessionTarget() error = %v", err)
	}
	if target.Address != "10.0.0.5:2222" {
		t.Fatalf("resolveSSHSessionTarget() address = %q, want %q", target.Address, "10.0.0.5:2222")
	}
	if target.IdentityFile != "/tmp/id_builder" {
		t.Fatalf("resolveSSHSessionTarget() identity file = %q, want %q", target.IdentityFile, "/tmp/id_builder")
	}
}

func TestSSHRunSessionDepsEnsureDaemonAndDial(t *testing.T) {
	t.Parallel()

	state := &sshSessionState{}
	sshClient := new(ssh.Client)
	sshConfig := &ssh.ClientConfig{}
	connClient, connServer := net.Pipe()
	t.Cleanup(func() {
		_ = connClient.Close()
		_ = connServer.Close()
	})

	var calls []string
	deps := sshRunSessionDeps(sshSessionTarget{
		SSHTarget: sshutil.SSHTarget{
			User:    "alice",
			Host:    "builder",
			Port:    "22",
			Session: "work",
		},
		Address:      "10.0.0.5:2222",
		IdentityFile: "/tmp/id_builder",
	}, state, sshRunSessionOps{
		buildSSHConfig: func(user, identityFile string) (*ssh.ClientConfig, error) {
			calls = append(calls, "build-config")
			if user != "alice" {
				t.Fatalf("buildSSHConfig user = %q, want alice", user)
			}
			if identityFile != "/tmp/id_builder" {
				t.Fatalf("buildSSHConfig identityFile = %q, want /tmp/id_builder", identityFile)
			}
			return sshConfig, nil
		},
		sshDial: func(network, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
			calls = append(calls, "ssh-dial")
			if network != "tcp" {
				t.Fatalf("sshDial network = %q, want tcp", network)
			}
			if addr != "10.0.0.5:2222" {
				t.Fatalf("sshDial addr = %q, want 10.0.0.5:2222", addr)
			}
			if cfg != sshConfig {
				t.Fatal("sshDial received unexpected client config")
			}
			return sshClient, nil
		},
		sshOutput: func(client *ssh.Client, cmd string) (string, error) {
			calls = append(calls, "ssh-output:"+cmd)
			if client != sshClient {
				t.Fatal("sshOutput received unexpected client")
			}
			switch cmd {
			case "id -u":
				return "1001", nil
			case "test -S /tmp/amux-1001/work && echo ok":
				return "ok", nil
			default:
				t.Fatalf("sshOutput cmd = %q, want id -u or socket probe", cmd)
				return "", nil
			}
		},
		deployBinary: func(client *ssh.Client, buildHash string) error {
			calls = append(calls, "deploy")
			if client != sshClient {
				t.Fatal("deployBinary received unexpected client")
			}
			if buildHash == "" {
				t.Fatal("deployBinary buildHash should not be empty")
			}
			return nil
		},
		ensureRemoteServer: func(client *ssh.Client, sockPath, sessionName string) error {
			calls = append(calls, "ensure-remote")
			if client != sshClient {
				t.Fatal("ensureRemoteServer received unexpected client")
			}
			if sockPath != "/tmp/amux-1001/work" {
				t.Fatalf("ensureRemoteServer sockPath = %q, want /tmp/amux-1001/work", sockPath)
			}
			if sessionName != "work" {
				t.Fatalf("ensureRemoteServer sessionName = %q, want work", sessionName)
			}
			return nil
		},
		dialRemoteSocket: func(client *ssh.Client, sockPath string) (net.Conn, error) {
			calls = append(calls, "dial-remote-socket")
			if client != sshClient {
				t.Fatal("dialRemoteSocket received unexpected client")
			}
			if sockPath != "/tmp/amux-1001/work" {
				t.Fatalf("dialRemoteSocket sockPath = %q, want /tmp/amux-1001/work", sockPath)
			}
			return connClient, nil
		},
	})

	if err := deps.ensureDaemon("work", 200*time.Millisecond); err != nil {
		t.Fatalf("ensureDaemon() error = %v", err)
	}
	conn, err := deps.dial("unix", "/tmp/ignored")
	if err != nil {
		t.Fatalf("dial() error = %v", err)
	}
	if conn != connClient {
		t.Fatal("dial() returned unexpected connection")
	}

	wantCalls := []string{
		"build-config",
		"ssh-dial",
		"ssh-output:id -u",
		"deploy",
		"ensure-remote",
		"ssh-output:test -S /tmp/amux-1001/work && echo ok",
		"dial-remote-socket",
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
	sshClient := new(ssh.Client)

	deps := sshRunSessionDeps(sshSessionTarget{
		SSHTarget: sshutil.SSHTarget{
			User:    "alice",
			Host:    "builder",
			Port:    "22",
			Session: "main",
		},
		Address: "builder:22",
	}, state, sshRunSessionOps{
		buildSSHConfig: func(string, string) (*ssh.ClientConfig, error) { return &ssh.ClientConfig{}, nil },
		sshDial:        func(string, string, *ssh.ClientConfig) (*ssh.Client, error) { return sshClient, nil },
		sshOutput: func(_ *ssh.Client, cmd string) (string, error) {
			switch cmd {
			case "id -u":
				return "1000", nil
			case "test -S /tmp/amux-1000/main && echo ok":
				return "ok", nil
			default:
				return "", nil
			}
		},
		deployBinary:       func(*ssh.Client, string) error { return errors.New("deploy failed") },
		ensureRemoteServer: func(*ssh.Client, string, string) error { return nil },
		dialRemoteSocket:   func(*ssh.Client, string) (net.Conn, error) { return nil, nil },
	})

	if err := deps.ensureDaemon("main", 200*time.Millisecond); err != nil {
		t.Fatalf("ensureDaemon() error = %v, want deploy failure to be ignored", err)
	}
}
