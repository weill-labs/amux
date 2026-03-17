package test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestSSHKey generates a throwaway ed25519 key pair, appends the
// public key to ~/.ssh/authorized_keys, and returns the private key path
// plus a cleanup function that removes the key from authorized_keys.
// This allows the Go crypto/ssh client to authenticate to localhost
// without depending on the macOS Keychain SSH agent.
func setupTestSSHKey(t *testing.T) (keyFile string, cleanup func()) {
	t.Helper()

	// Check that sshd is running on localhost
	if err := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=2", "localhost", "true").Run(); err != nil {
		t.Fatalf("SSH to localhost not available (is Remote Login enabled?): %v", err)
	}

	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "id_test")

	// Generate key pair (no passphrase)
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-f", keyPath, "-N", "", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen failed: %v\n%s", err, out)
	}

	// Read public key
	pubKey, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatalf("reading public key: %v", err)
	}
	pubKeyStr := strings.TrimSpace(string(pubKey))

	// Append to authorized_keys
	authKeysPath := filepath.Join(os.Getenv("HOME"), ".ssh", "authorized_keys")
	os.MkdirAll(filepath.Dir(authKeysPath), 0700)

	f, err := os.OpenFile(authKeysPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("opening authorized_keys: %v", err)
	}
	if _, err := fmt.Fprintf(f, "\n%s\n", pubKeyStr); err != nil {
		f.Close()
		t.Fatalf("writing to authorized_keys: %v", err)
	}
	f.Close()

	// Verify the key works with the system ssh command
	testOut, testErr := exec.Command("ssh", "-i", keyPath,
		"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no",
		"localhost", "echo", "KEY_OK").CombinedOutput()
	if testErr != nil {
		t.Fatalf("test SSH key doesn't work: %v\n%s\nkey: %s\nauthorized_keys: %s",
			testErr, testOut, keyPath, authKeysPath)
	}
	t.Logf("test SSH key verified: %s", strings.TrimSpace(string(testOut)))

	cleanup = func() {
		// Remove the test key from authorized_keys
		data, err := os.ReadFile(authKeysPath)
		if err != nil {
			return
		}
		lines := strings.Split(string(data), "\n")
		var kept []string
		for _, line := range lines {
			if strings.TrimSpace(line) != pubKeyStr {
				kept = append(kept, line)
			}
		}
		os.WriteFile(authKeysPath, []byte(strings.Join(kept, "\n")), 0600)
	}

	return keyPath, cleanup
}

// remoteLocalhostConfig returns a TOML config string with a "test-remote"
// host pointing at localhost using the given identity file.
func remoteLocalhostConfig(identityFile string) string {
	user := os.Getenv("USER")
	if user == "" {
		user = "root"
	}
	return fmt.Sprintf(`
[hosts.test-remote]
type = "remote"
user = "%s"
address = "localhost"
identity_file = "%s"
`, user, identityFile)
}
