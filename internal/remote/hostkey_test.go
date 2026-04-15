package remote

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	charmlog "github.com/charmbracelet/log"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/weill-labs/amux/internal/auditlog"
	"github.com/weill-labs/amux/internal/config"
)

// testHostKey generates a random ed25519 SSH public key for testing.
func testHostKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// fakeAddr implements net.Addr for testing host key callbacks.
type fakeAddr struct{ addr string }

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return a.addr }

func TestHostKeyTOFU(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	key := testHostKey(t)
	cb := hostKeyCallback(path)

	err := cb("example.com:22", fakeAddr{"1.2.3.4:22"}, key)
	if err != nil {
		t.Fatalf("TOFU should accept unknown host, got: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "example.com") {
		t.Errorf("known_hosts should contain hostname, got:\n%s", data)
	}
}

func TestHostKeyKnownMatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	key := testHostKey(t)

	// Pre-write the entry
	if err := appendKnownHost(path, "example.com:22", key); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)

	cb := hostKeyCallback(path)
	err := cb("example.com:22", fakeAddr{"1.2.3.4:22"}, key)
	if err != nil {
		t.Fatalf("known host should be accepted, got: %v", err)
	}

	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("file should not be modified when host key matches")
	}
}

func TestHostKeyMismatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	keyA := testHostKey(t)
	keyB := testHostKey(t)

	if err := appendKnownHost(path, "example.com:22", keyA); err != nil {
		t.Fatal(err)
	}

	cb := hostKeyCallback(path)
	err := cb("example.com:22", fakeAddr{"1.2.3.4:22"}, keyB)
	if err == nil {
		t.Fatal("mismatched key should be rejected")
	}
	msg := err.Error()
	if !strings.Contains(msg, "CHANGED") {
		t.Errorf("error should mention CHANGED, got: %s", msg)
	}
	if !strings.Contains(msg, "ssh-keygen -R") {
		t.Errorf("error should suggest ssh-keygen -R, got: %s", msg)
	}
}

func TestHostKeyRevoked(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	key := testHostKey(t)

	line := "@revoked " + knownhosts.Line([]string{"example.com:22"}, key)
	if err := os.WriteFile(path, []byte(line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cb := hostKeyCallback(path)
	err := cb("example.com:22", fakeAddr{"1.2.3.4:22"}, key)
	if err == nil {
		t.Fatal("revoked key should be rejected")
	}
}

func TestHostKeyNonStandardPort(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	key := testHostKey(t)

	// knownhosts.Normalize produces [host]:port for non-standard ports
	hostname := knownhosts.Normalize("example.com:2222")
	cb := hostKeyCallback(path)

	err := cb(hostname, fakeAddr{"1.2.3.4:2222"}, key)
	if err != nil {
		t.Fatalf("TOFU should accept, got: %v", err)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "[example.com]:2222") {
		t.Errorf("known_hosts should contain bracketed host:port, got:\n%s", data)
	}

	// Re-verify lookup works
	err = cb(hostname, fakeAddr{"1.2.3.4:2222"}, key)
	if err != nil {
		t.Fatalf("known host with non-standard port should match, got: %v", err)
	}
}

func TestHostKeyMissingDirCreated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "known_hosts")
	key := testHostKey(t)
	cb := hostKeyCallback(path)

	err := cb("example.com:22", fakeAddr{"1.2.3.4:22"}, key)
	if err != nil {
		t.Fatalf("TOFU with missing dir should succeed, got: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "sub", "dir"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0700 {
		t.Errorf("dir permissions = %o, want 0700", perm)
	}
}

func TestHostKeyTOFUAuditLogsAtInfo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	key := testHostKey(t)

	var buf bytes.Buffer
	logger := auditlog.New(&buf, auditlog.Options{
		Format: auditlog.FormatJSON,
		Level:  charmlog.InfoLevel,
	})

	cb := hostKeyCallback(path, logger)
	if err := cb("example.com:22", fakeAddr{"1.2.3.4:22"}, key); err != nil {
		t.Fatalf("TOFU should accept unknown host, got: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, `"event":"ssh_hostkey_trust"`) {
		t.Fatalf("output %q missing ssh_hostkey_trust event", output)
	}
	if !strings.Contains(output, `"level":"info"`) {
		t.Fatalf("output %q missing info level", output)
	}
}

func TestHostKeyMalformedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")

	// Write invalid content that knownhosts.New will reject
	if err := os.WriteFile(path, []byte("not a valid known_hosts entry with enough fields x x x\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cb := hostKeyCallback(path)
	key := testHostKey(t)
	err := cb("example.com:22", fakeAddr{"1.2.3.4:22"}, key)

	// The callback should either reject with a parse error or fall through
	// to TOFU (since the malformed line won't match). Either way it should
	// not panic. If knownhosts.New succeeds but finds no match, TOFU fires.
	// We accept both outcomes — the critical thing is no silent accept
	// without either matching or writing the key.
	if err != nil {
		return // parse error is fine
	}
	// If no error, the key must have been written (TOFU)
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "example.com:22") {
		t.Error("if no error, TOFU should have written the key")
	}
}

func TestHostKeyWriteFailureRejects(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "readonly", "known_hosts")

	// Create the parent dir as read-only so the write fails
	roDir := filepath.Join(dir, "readonly")
	if err := os.MkdirAll(roDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(roDir, 0700) })

	cb := hostKeyCallback(path)
	key := testHostKey(t)
	err := cb("example.com:22", fakeAddr{"1.2.3.4:22"}, key)
	if err == nil {
		t.Fatal("write failure should reject the connection, not silently accept")
	}
}

func TestDefaultKnownHostsPathEmptyHome(t *testing.T) {
	t.Setenv("HOME", "")
	// Also unset common fallbacks so os.UserHomeDir fails
	t.Setenv("USERPROFILE", "")

	cb := hostKeyCallback("")
	key := testHostKey(t)
	err := cb("example.com:22", fakeAddr{"1.2.3.4:22"}, key)
	if err == nil {
		t.Fatal("empty HOME should cause rejection, not silent accept")
	}
}

func TestDefaultKnownHostsPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	got, err := defaultKnownHostsPath()
	if err != nil {
		t.Fatalf("defaultKnownHostsPath() error = %v", err)
	}
	want := filepath.Join(tmpDir, ".ssh", "known_hosts")
	if got != want {
		t.Fatalf("defaultKnownHostsPath() = %q, want %q", got, want)
	}
}

func TestBuildSSHConfigWiresHostKeyCallback(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("SSH_AUTH_SOCK", "")

	// Write an SSH key so buildSSHConfig has auth
	sshDir := filepath.Join(tmpDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatal(err)
	}
	writeTestKey(t, filepath.Join(sshDir, "id_ed25519"))

	hc := NewHostConn("test", configStubHost(), "hash", nil, nil, nil)
	defer hc.Close()

	cfg, err := hc.buildSSHConfig()
	if err != nil {
		t.Fatalf("buildSSHConfig: %v", err)
	}

	// The callback should do TOFU and write to ~/.ssh/known_hosts
	key := testHostKey(t)
	err = cfg.HostKeyCallback("example.com:22", fakeAddr{"1.2.3.4:22"}, key)
	if err != nil {
		t.Fatalf("wired callback should TOFU-accept, got: %v", err)
	}

	khPath := filepath.Join(sshDir, "known_hosts")
	data, err := os.ReadFile(khPath)
	if err != nil {
		t.Fatalf("known_hosts should exist after TOFU: %v", err)
	}
	if !strings.Contains(string(data), "example.com") {
		t.Error("known_hosts should contain the TOFU'd host")
	}
}

func TestBuildSSHConfigHostKeyCallbackReloadsKnownHostsFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("SSH_AUTH_SOCK", "")

	sshDir := filepath.Join(tmpDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatal(err)
	}
	writeTestKey(t, filepath.Join(sshDir, "id_ed25519"))

	hc := NewHostConn("test", configStubHost(), "hash", nil, nil, nil)
	defer hc.Close()

	cfg, err := hc.buildSSHConfig()
	if err != nil {
		t.Fatalf("buildSSHConfig: %v", err)
	}

	oldKey := testHostKey(t)
	newKey := testHostKey(t)
	khPath := filepath.Join(sshDir, "known_hosts")

	// Write an initial host key, then keep using the same callback after the
	// file is rewritten to prove it does not cache stale known_hosts contents.
	oldLine := knownhosts.Line([]string{"example.com:22"}, oldKey)
	if err := os.WriteFile(khPath, []byte(oldLine+"\n"), 0600); err != nil {
		t.Fatalf("writing initial known_hosts: %v", err)
	}
	if err := cfg.HostKeyCallback("example.com:22", fakeAddr{"1.2.3.4:22"}, oldKey); err != nil {
		t.Fatalf("initial host key should match, got: %v", err)
	}

	newLine := knownhosts.Line([]string{"example.com:22"}, newKey)
	if err := os.WriteFile(khPath, []byte(newLine+"\n"), 0600); err != nil {
		t.Fatalf("rewriting known_hosts: %v", err)
	}
	if err := cfg.HostKeyCallback("example.com:22", fakeAddr{"1.2.3.4:22"}, newKey); err != nil {
		t.Fatalf("rewritten known_hosts should be re-read by the same callback, got: %v", err)
	}

	err = cfg.HostKeyCallback("example.com:22", fakeAddr{"1.2.3.4:22"}, oldKey)
	if err == nil {
		t.Fatal("stale host key should be rejected after known_hosts rewrite")
	}
	if !strings.Contains(err.Error(), "CHANGED") {
		t.Fatalf("stale host key error = %q, want changed-host warning", err)
	}
}

func TestBuildSSHConfigInsecureEnvVar(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv("AMUX_SSH_INSECURE", "1")

	sshDir := filepath.Join(tmpDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatal(err)
	}
	writeTestKey(t, filepath.Join(sshDir, "id_ed25519"))

	hc := NewHostConn("test", configStubHost(), "hash", nil, nil, nil)
	defer hc.Close()

	cfg, err := hc.buildSSHConfig()
	if err != nil {
		t.Fatalf("buildSSHConfig: %v", err)
	}

	// Insecure callback should accept any key without writing known_hosts
	key := testHostKey(t)
	err = cfg.HostKeyCallback("anything:22", fakeAddr{"9.9.9.9:22"}, key)
	if err != nil {
		t.Fatalf("insecure callback should accept, got: %v", err)
	}

	khPath := filepath.Join(sshDir, "known_hosts")
	if _, err := os.Stat(khPath); err == nil {
		t.Error("insecure mode should not create known_hosts")
	}
}

// configStubHost returns a minimal config.Host for wiring tests.
func configStubHost() config.Host {
	return config.Host{Type: "remote", Address: "10.0.0.1"}
}
