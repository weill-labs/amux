package remote

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/crypto/ssh"
)

// DeployBinary ensures the remote host has a matching amux binary.
// Strategy:
//  1. Remote hash matches local → skip (already up to date)
//  2. Same architecture → upload local binary
//  3. Cross architecture → local cross-compile, then upload
//  4. Cross architecture → download from GitHub releases (remote curl first, then local download + upload)
func DeployBinary(client *ssh.Client, buildHash string) error {
	if os.Getenv("AMUX_NO_DEPLOY") == "1" {
		return nil
	}

	// Check if remote binary exists and matches our build hash
	remoteHash, err := remoteBuildHash(client)
	if err == nil && remoteHash == buildHash {
		return nil // up to date
	}

	// Detect remote architecture
	remoteUname, err := sshOutput(client, "uname -sm")
	if err != nil {
		return fmt.Errorf("detecting remote arch: %w", err)
	}
	remoteOS, remoteArch, err := parseUnameArch(remoteUname)
	if err != nil {
		return fmt.Errorf("parsing remote arch %q: %w", remoteUname, err)
	}

	localOS := runtime.GOOS
	localArch := runtime.GOARCH

	// Same arch: upload local binary directly
	if localOS == remoteOS && localArch == remoteArch {
		localExe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("finding local executable: %w", err)
		}
		return uploadBinary(client, localExe)
	}

	// Cross arch: try local cross-compile
	if err := crossCompileAndUpload(client, remoteOS, remoteArch); err == nil {
		return nil
	}

	// Cross arch: try GitHub release download
	if err := downloadReleaseBinary(client, remoteOS, remoteArch, buildHash); err == nil {
		return nil
	}

	return fmt.Errorf("cross-arch deploy failed: local=%s/%s, remote=%s/%s", localOS, localArch, remoteOS, remoteArch)
}

// parseUnameArch maps `uname -sm` output to GOOS/GOARCH values.
func parseUnameArch(unameSM string) (goos, goarch string, err error) {
	parts := strings.Fields(unameSM)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("expected 2 fields, got %d", len(parts))
	}

	switch strings.ToLower(parts[0]) {
	case "linux":
		goos = "linux"
	case "darwin":
		goos = "darwin"
	default:
		return "", "", fmt.Errorf("unsupported OS: %s", parts[0])
	}

	switch parts[1] {
	case "x86_64", "amd64":
		goarch = "amd64"
	case "aarch64", "arm64":
		goarch = "arm64"
	default:
		return "", "", fmt.Errorf("unsupported arch: %s", parts[1])
	}

	return goos, goarch, nil
}

// crossCompileAndUpload builds amux for the target OS/arch via `go build` and uploads it.
func crossCompileAndUpload(client *ssh.Client, goos, goarch string) error {
	// Find the module root (where go.mod lives)
	localExe, err := os.Executable()
	if err != nil {
		return err
	}
	modRoot := findModuleRoot(filepath.Dir(localExe))
	if modRoot == "" {
		return fmt.Errorf("could not find go.mod")
	}

	tmpFile, err := os.CreateTemp("", fmt.Sprintf("amux-%s-%s-*", goos, goarch))
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpBin := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpBin)

	cmd := exec.Command("go", "build", "-o", tmpBin, ".")
	cmd.Dir = modRoot
	cmd.Env = append(os.Environ(),
		"GOOS="+goos,
		"GOARCH="+goarch,
		"CGO_ENABLED=0",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cross-compile: %w: %s", err, out)
	}

	return uploadBinary(client, tmpBin)
}

// findModuleRoot walks up from dir looking for go.mod.
func findModuleRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// downloadReleaseBinary tries to download a pre-built binary from GitHub releases.
// Tries remote curl first (fastest), falls back to local download + upload.
// NOTE: version must be a semver tag (e.g., "0.1.0") for the URL to resolve.
// During development, buildHash is a git commit hash, so this path will 404 —
// cross-compile is the primary stopgap until tagged releases are published.
func downloadReleaseBinary(client *ssh.Client, goos, goarch, version string) error {
	archiveName := fmt.Sprintf("amux_%s_%s_%s.tar.gz", version, goos, goarch)
	url := fmt.Sprintf("https://github.com/weill-labs/amux/releases/download/v%s/%s", version, archiveName)

	// Try remote curl: download directly on the remote host
	remoteCmd := fmt.Sprintf(
		`mkdir -p ~/.local/bin && cd /tmp && curl -fsSL %q -o %q && tar xzf %q amux && mv amux ~/.local/bin/amux && chmod +x ~/.local/bin/amux && rm -f %q`,
		url, archiveName, archiveName, archiveName,
	)
	if err := sshRunErr(client, remoteCmd); err == nil {
		return nil
	}

	// Fallback: download locally and upload
	tmpDir, err := os.MkdirTemp("", "amux-deploy-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, archiveName)
	dlCmd := exec.Command("curl", "-fsSL", url, "-o", archivePath)
	if out, err := dlCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("downloading release: %w: %s", err, out)
	}

	binPath := filepath.Join(tmpDir, "amux")
	extractCmd := exec.Command("tar", "xzf", archivePath, "-C", tmpDir, "amux")
	if out, err := extractCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("extracting release: %w: %s", err, out)
	}

	return uploadBinary(client, binPath)
}

// remoteBuildHash checks the remote amux binary's build hash.
// Tries ~/.local/bin/amux first (deploy location), then PATH.
func remoteBuildHash(client *ssh.Client) (string, error) {
	out, err := sshOutput(client, `AMUX=$(command -v ~/.local/bin/amux 2>/dev/null || command -v amux 2>/dev/null || echo ""); [ -n "$AMUX" ] && "$AMUX" version --hash 2>/dev/null`)
	if err != nil || out == "" {
		return "", fmt.Errorf("amux not found on remote")
	}
	return out, nil
}

// uploadBinary uploads a local file to ~/.local/bin/amux on the remote.
func uploadBinary(client *ssh.Client, localPath string) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("reading local binary: %w", err)
	}

	sshRun(client, "mkdir -p ~/.local/bin")

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

// sshRunErr runs a command on the remote and returns the error.
func sshRunErr(client *ssh.Client, cmd string) error {
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	return sess.Run(cmd)
}
