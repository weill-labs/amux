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

const remoteInstallPath = "$HOME/.local/bin/amux"

type commandFactory func(name string, args ...string) *exec.Cmd

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

func crossCompileAndUpload(client *ssh.Client, goos, goarch string) error {
	return crossCompileAndUploadWith(client, goos, goarch, os.Executable, exec.Command)
}

// crossCompileAndUploadWith builds amux for the target OS/arch via `go build`
// and uploads it using injectable dependencies for testing.
func crossCompileAndUploadWith(client *ssh.Client, goos, goarch string, executablePath func() (string, error), makeCmd commandFactory) error {
	// Find the module root (where go.mod lives)
	localExe, err := executablePath()
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

	cmd := makeCmd("go", "build", "-o", tmpBin, ".")
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

func downloadReleaseBinary(client *ssh.Client, goos, goarch, version string) error {
	return downloadReleaseBinaryWith(client, goos, goarch, version, releaseBinaryURL, exec.Command)
}

// downloadReleaseBinaryWith downloads a pre-built binary from a release URL.
// Tries remote curl first (fastest), falls back to local download + upload.
func downloadReleaseBinaryWith(client *ssh.Client, goos, goarch, version string, releaseURL func(version, goos, goarch string) string, makeCmd commandFactory) error {
	archiveName := fmt.Sprintf("amux_%s_%s_%s.tar.gz", version, goos, goarch)
	url := releaseURL(version, goos, goarch)

	// Try remote curl: download directly on the remote host
	remoteCmd := remoteReleaseInstallCmd(url, archiveName)
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
	dlCmd := makeCmd("curl", "-fsSL", url, "-o", archivePath)
	if out, err := dlCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("downloading release: %w: %s", err, out)
	}

	binPath := filepath.Join(tmpDir, "amux")
	extractCmd := makeCmd("tar", "xzf", archivePath, "-C", tmpDir, "amux")
	if out, err := extractCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("extracting release: %w: %s", err, out)
	}

	return uploadBinary(client, binPath)
}

// releaseBinaryURL returns the GitHub releases URL for a published amux archive.
// NOTE: version must be a semver tag (e.g., "0.1.0") for the URL to resolve.
// During development, buildHash is a git commit hash, so this path will 404;
// cross-compile is the primary stopgap until tagged releases are published.
func releaseBinaryURL(version, goos, goarch string) string {
	archiveName := fmt.Sprintf("amux_%s_%s_%s.tar.gz", version, goos, goarch)
	return fmt.Sprintf("https://github.com/weill-labs/amux/releases/download/v%s/%s", version, archiveName)
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

	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()

	sess.Stdin = bytes.NewReader(data)
	if err := sess.Run(remoteInstallStdinCmd(remoteInstallPath)); err != nil {
		return fmt.Errorf("uploading binary: %w", err)
	}

	return nil
}

func remoteInstallStdinCmd(destPath string) string {
	return fmt.Sprintf(
		`set -eu; dir=$(dirname %q); mkdir -p "$dir"; tmp=$(mktemp "$dir/.amux.tmp.XXXXXX"); cleanup() { rm -f "$tmp"; }; trap cleanup EXIT; cat > "$tmp"; chmod +x "$tmp"; mv "$tmp" %q; trap - EXIT`,
		destPath, destPath,
	)
}

func remoteReleaseInstallCmd(url, archiveName string) string {
	return fmt.Sprintf(
		`set -eu; dir=$(dirname %q); mkdir -p "$dir"; tmp=$(mktemp "$dir/.amux.tmp.XXXXXX"); archive=$(mktemp "/tmp/%s.XXXXXX"); extract=$(mktemp -d "/tmp/amux-extract.XXXXXX"); cleanup() { rm -f "$tmp" "$archive"; rm -rf "$extract"; }; trap cleanup EXIT; curl -fsSL %q -o "$archive"; tar xzf "$archive" -C "$extract" amux; chmod +x "$extract/amux"; mv "$extract/amux" "$tmp"; mv "$tmp" %q; trap - EXIT`,
		remoteInstallPath, archiveName, url, remoteInstallPath,
	)
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
