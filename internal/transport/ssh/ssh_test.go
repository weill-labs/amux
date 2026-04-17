package ssh

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestNormalizeAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		addr string
		want string
	}{
		{name: "bare hostname", addr: "myhost", want: "myhost:22"},
		{name: "bare ip", addr: "10.0.0.1", want: "10.0.0.1:22"},
		{name: "address with port", addr: "10.0.0.1:2222", want: "10.0.0.1:2222"},
		{name: "bare ipv6", addr: "::1", want: "::1:22"},
		{name: "bracketed ipv6 with port", addr: "[::1]:2200", want: "[::1]:2200"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := NormalizeAddr(tt.addr); got != tt.want {
				t.Fatalf("NormalizeAddr(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestRemoteSocketPath(t *testing.T) {
	t.Parallel()

	if got := RemoteSocketPath("1000", "main@test"); got != "/tmp/amux-1000/main@test" {
		t.Fatalf("RemoteSocketPath() = %q, want %q", got, "/tmp/amux-1000/main@test")
	}
}

func TestBuildSSHConfigUsesDefaultUserAndTOFUCallback(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("SSH_AUTH_SOCK", "")

	sshDir := filepath.Join(tmpDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}
	writeTestKey(t, filepath.Join(sshDir, "id_ed25519"))

	cfg, err := BuildSSHConfig("", "")
	if err != nil {
		t.Fatalf("BuildSSHConfig() error = %v", err)
	}
	wantUser := DefaultSSHUser()
	if cfg.User != wantUser {
		t.Fatalf("BuildSSHConfig() user = %q, want %q", cfg.User, wantUser)
	}

	key := testHostKey(t)
	if err := cfg.HostKeyCallback("example.com:22", fakeAddr{"1.2.3.4:22"}, key); err != nil {
		t.Fatalf("HostKeyCallback() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(sshDir, "known_hosts"))
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	if !strings.Contains(string(data), "example.com") {
		t.Fatalf("known_hosts = %q, want entry for example.com", string(data))
	}
}

func TestBuildSSHConfigInsecureEnvVar(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv("AMUX_SSH_INSECURE", "1")

	sshDir := filepath.Join(tmpDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}
	writeTestKey(t, filepath.Join(sshDir, "id_ed25519"))

	cfg, err := BuildSSHConfig("", "")
	if err != nil {
		t.Fatalf("BuildSSHConfig() error = %v", err)
	}

	key := testHostKey(t)
	if err := cfg.HostKeyCallback("example.com:22", fakeAddr{"1.2.3.4:22"}, key); err != nil {
		t.Fatalf("HostKeyCallback() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(sshDir, "known_hosts")); err == nil {
		t.Fatal("known_hosts should not be created in insecure mode")
	}
}

func TestSSHOutput(t *testing.T) {
	t.Parallel()

	ts := startTestSSH(t)

	out, err := SSHOutput(ts.Client, "echo hello")
	if err != nil {
		t.Fatalf("SSHOutput() error = %v", err)
	}
	if out != "hello" {
		t.Fatalf("SSHOutput() = %q, want %q", out, "hello")
	}
}

func TestHostKeyCallbackRejectsChangedKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	keyA := testHostKey(t)
	keyB := testHostKey(t)

	if err := AppendKnownHost(path, "example.com:22", keyA); err != nil {
		t.Fatalf("AppendKnownHost() error = %v", err)
	}

	cb := HostKeyCallback(path)
	err := cb("example.com:22", fakeAddr{"1.2.3.4:22"}, keyB)
	if err == nil {
		t.Fatal("HostKeyCallback() error = nil, want changed-host failure")
	}
	if !strings.Contains(err.Error(), "CHANGED") {
		t.Fatalf("HostKeyCallback() error = %q, want changed-host warning", err)
	}
	wantCmd := fmt.Sprintf("ssh-keygen -f %s -R example.com", path)
	if !strings.Contains(err.Error(), wantCmd) {
		t.Fatalf("HostKeyCallback() error = %q, want %q", err, wantCmd)
	}
}

func TestDefaultKnownHostsPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	got, err := DefaultKnownHostsPath()
	if err != nil {
		t.Fatalf("DefaultKnownHostsPath() error = %v", err)
	}
	want := filepath.Join(tmpDir, ".ssh", "known_hosts")
	if got != want {
		t.Fatalf("DefaultKnownHostsPath() = %q, want %q", got, want)
	}
}

func TestEnsureRemoteServer(t *testing.T) {
	t.Parallel()

	ts := startTestSSH(t)
	writeFakeRemoteAmux(t, ts)

	sessionName := filepath.Base(t.TempDir())
	sockPath := RemoteSocketPath("1000", sessionName)
	actualSockPath := RemoteSocketPath(fmt.Sprintf("%d", os.Getuid()), sessionName)
	_ = os.Remove(sockPath)
	_ = os.Remove(actualSockPath)
	_ = os.MkdirAll(filepath.Dir(sockPath), 0o755)
	t.Cleanup(func() {
		_ = os.Remove(sockPath)
		_ = os.Remove(actualSockPath)
	})

	if err := EnsureRemoteServer(ts.Client, sockPath, sessionName); err != nil {
		t.Fatalf("EnsureRemoteServer() error = %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		out, err := SSHOutput(ts.Client, fmt.Sprintf("test -S %s && echo ok", actualSockPath))
		if err == nil && out == "ok" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("remote socket %q did not appear", actualSockPath)
}

func TestDialRemoteSocketReportsFailure(t *testing.T) {
	t.Parallel()

	ts := startTestSSH(t)

	_, err := DialRemoteSocket(ts.Client, "/tmp/amux-1000/missing")
	if err == nil {
		t.Fatal("DialRemoteSocket() error = nil, want failure")
	}
	if !strings.Contains(err.Error(), "TCP fallback failed") {
		t.Fatalf("DialRemoteSocket() error = %q, want TCP fallback failure", err)
	}
}

type fakeAddr struct{ addr string }

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return a.addr }

func testHostKey(t *testing.T) ssh.PublicKey {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("NewPublicKey() error = %v", err)
	}
	return key
}

type testSSH struct {
	Client  *ssh.Client
	HomeDir string
	Addr    string
}

func startTestSSH(t *testing.T) *testSSH {
	t.Helper()

	pubEd, privEd, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pubEd)
	if err != nil {
		t.Fatalf("converting public key: %v", err)
	}

	authorizedKeyBytes := sshPub.Marshal()
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating host key: %v", err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("creating host signer: %v", err)
	}

	srvCfg := &ssh.ServerConfig{
		MaxAuthTries: 20,
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), authorizedKeyBytes) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unauthorized")
		},
	}
	srvCfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	var wg sync.WaitGroup
	t.Cleanup(func() {
		ln.Close()
		wg.Wait()
	})

	execEnv := os.Environ()
	homeDir := t.TempDir()
	for i, e := range execEnv {
		if strings.HasPrefix(e, "HOME=") {
			execEnv[i] = "HOME=" + homeDir
		}
		if strings.HasPrefix(e, "PATH=") {
			execEnv[i] = "PATH=" + filepath.Join(homeDir, ".local", "bin") + ":/usr/bin:/bin"
		}
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			tcpConn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				testHandleSSHConn(tcpConn, srvCfg, execEnv)
			}()
		}
	}()

	signer, err := ssh.NewSignerFromKey(privEd)
	if err != nil {
		t.Fatalf("creating signer: %v", err)
	}
	clientCfg := &ssh.ClientConfig{
		User:            "testuser",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	client, err := ssh.Dial("tcp", ln.Addr().String(), clientCfg)
	if err != nil {
		t.Fatalf("SSH dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	return &testSSH{
		Client:  client,
		HomeDir: homeDir,
		Addr:    ln.Addr().String(),
	}
}

func testHandleSSHConn(tcpConn net.Conn, config *ssh.ServerConfig, execEnv []string) {
	defer tcpConn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(tcpConn, config)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			ignoreReject(newChannel, ssh.UnknownChannelType, "unsupported")
			continue
		}
		ch, chReqs, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go testHandleSession(ch, chReqs, execEnv)
	}
}

func testHandleSession(ch ssh.Channel, reqs <-chan *ssh.Request, execEnv []string) {
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			if req.WantReply {
				ignoreReply(req, false)
			}
			continue
		}
		if len(req.Payload) < 4 {
			ignoreReply(req, false)
			continue
		}
		cmdLen := binary.BigEndian.Uint32(req.Payload[:4])
		command := string(req.Payload[4 : 4+cmdLen])
		ignoreReply(req, true)

		cmd := exec.Command("sh", "-c", command)
		cmd.Env = execEnv
		cmd.Stdout = ch
		cmd.Stderr = ch.Stderr()
		cmd.Stdin = ch

		exitCode := 0
		if err := cmd.Run(); err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}

		exitMsg := make([]byte, 4)
		binary.BigEndian.PutUint32(exitMsg, uint32(exitCode))
		ignoreSendRequest(ch, "exit-status", false, exitMsg)
		return
	}
}

func ignoreReject(newChannel ssh.NewChannel, reason ssh.RejectionReason, message string) {
	_ = newChannel.Reject(reason, message)
}

func ignoreReply(req *ssh.Request, ok bool) {
	_ = req.Reply(ok, nil)
}

func ignoreSendRequest(ch ssh.Channel, name string, wantReply bool, payload []byte) {
	_, _ = ch.SendRequest(name, wantReply, payload)
}

func writeTestKey(t *testing.T, path string) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("MarshalPrivateKey() error = %v", err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func writeFakeRemoteAmux(t *testing.T, ts *testSSH) {
	t.Helper()

	path := filepath.Join(ts.HomeDir, ".local", "bin", "amux")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fake amux dir: %v", err)
	}

	script := `#!/bin/sh
set -eu
case "${1:-}" in
  install-terminfo)
    exit 0
    ;;
  _server)
    session="${2:?missing session}"
    sock="/tmp/amux-$(id -u)/$session"
    mkdir -p "$(dirname "$sock")"
    python3 - "$sock" <<'PY' >/dev/null 2>&1 &
import os, socket, sys, time
sock = sys.argv[1]
try:
    os.unlink(sock)
except FileNotFoundError:
    pass
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.bind(sock)
s.listen(1)
time.sleep(5)
s.close()
PY
    exit 0
    ;;
esac
exit 1
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake amux script: %v", err)
	}
}
