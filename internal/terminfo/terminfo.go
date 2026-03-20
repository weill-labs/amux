package terminfo

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const Name = "amux"

//go:embed amux.terminfo
var source string

var (
	userHomeDir     = os.UserHomeDir
	flockFile       = func(f *os.File, how int) error { return syscall.Flock(int(f.Fd()), how) }
	writeTempSource = func(f *os.File, content string) error {
		_, err := f.WriteString(content)
		return err
	}
	closeTempSource = func(f *os.File) error { return f.Close() }
)

// Install compiles the embedded amux terminfo entry into ~/.terminfo.
// It is safe to run repeatedly.
func Install() error {
	home, err := userHomeDir()
	if err != nil {
		return fmt.Errorf("finding home directory for %s terminfo install: %w", Name, err)
	}
	if home == "" {
		return fmt.Errorf("finding home directory for %s terminfo install: HOME is empty", Name)
	}

	installDir := filepath.Join(home, ".terminfo")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("creating %s terminfo dir %s: %w", Name, installDir, err)
	}
	lockFile, err := os.OpenFile(filepath.Join(installDir, ".amux.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s terminfo lock: %w", Name, err)
	}
	defer lockFile.Close()
	if err := flockFile(lockFile, syscall.LOCK_EX); err != nil {
		return fmt.Errorf("locking %s terminfo install: %w", Name, err)
	}
	defer flockFile(lockFile, syscall.LOCK_UN)

	tic, err := exec.LookPath("tic")
	if err != nil {
		return fmt.Errorf("installing %s terminfo requires `tic` in PATH", Name)
	}

	tmp, err := os.CreateTemp("", "amux-terminfo-*.src")
	if err != nil {
		return fmt.Errorf("creating temp terminfo source: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := writeTempSource(tmp, source); err != nil {
		closeTempSource(tmp)
		return fmt.Errorf("writing temp terminfo source: %w", err)
	}
	if err := closeTempSource(tmp); err != nil {
		return fmt.Errorf("closing temp terminfo source: %w", err)
	}

	cmd := exec.Command(tic, "-x", "-o", installDir, tmpPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("compiling %s terminfo: %w", Name, err)
		}
		return fmt.Errorf("compiling %s terminfo: %w: %s", Name, err, msg)
	}

	return nil
}

// EnsureInstalled installs the embedded terminfo entry if possible.
func EnsureInstalled() error {
	return Install()
}
