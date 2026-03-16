package remote

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/crypto/ssh"
)

// DeployBinary detects the remote OS/arch, checks if a matching amux binary
// exists, and uploads it if missing or outdated.
//
// Phase 1 supports same-arch only (e.g., darwin/arm64 → darwin/arm64).
// Cross-compilation support is planned for a future phase.
func DeployBinary(client *ssh.Client, buildHash string) error {
	// Check if remote binary exists and matches our build hash
	remoteHash, err := remoteBuildHash(client)
	if err == nil && remoteHash == buildHash {
		return nil // up to date
	}

	// Detect remote architecture
	remoteArch, err := sshOutput(client, "uname -sm")
	if err != nil {
		return fmt.Errorf("detecting remote arch: %w", err)
	}

	// Check local architecture matches
	localArch, err := exec.Command("uname", "-sm").Output()
	if err != nil {
		return fmt.Errorf("detecting local arch: %w", err)
	}
	if strings.TrimSpace(string(localArch)) != strings.TrimSpace(remoteArch) {
		return fmt.Errorf("cross-arch deploy not yet supported: local=%s, remote=%s",
			strings.TrimSpace(string(localArch)), strings.TrimSpace(remoteArch))
	}

	localExe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding local executable: %w", err)
	}

	return uploadBinary(client, localExe)
}

// remoteBuildHash checks the remote amux binary's build hash.
func remoteBuildHash(client *ssh.Client) (string, error) {
	out, err := sshOutput(client, "amux version --hash 2>/dev/null")
	if err != nil || out == "" {
		return "", fmt.Errorf("amux not found on remote")
	}
	return strings.TrimSpace(out), nil
}

// uploadBinary uploads the local amux binary to ~/.local/bin/amux on the remote.
func uploadBinary(client *ssh.Client, localPath string) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("reading local binary: %w", err)
	}

	// Create the target directory
	sshRun(client, "mkdir -p ~/.local/bin")

	// Upload via stdin + cat
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()

	sess.Stdin = bytes.NewReader(data)
	if err := sess.Run("cat > ~/.local/bin/amux && chmod +x ~/.local/bin/amux"); err != nil {
		return fmt.Errorf("uploading binary: %w", err)
	}

	return nil
}

// sshOutput runs a command on the remote and returns trimmed stdout.
func sshOutput(client *ssh.Client, cmd string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()

	out, err := sess.Output(cmd)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// sshRun runs a command on the remote, ignoring errors.
func sshRun(client *ssh.Client, cmd string) {
	sess, err := client.NewSession()
	if err != nil {
		return
	}
	defer sess.Close()
	sess.Run(cmd)
}
