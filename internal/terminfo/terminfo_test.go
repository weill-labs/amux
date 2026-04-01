package terminfo

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestInstallWritesEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := Install(); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if err := Install(); err != nil {
		t.Fatalf("Install() second run error = %v", err)
	}

	cmd := exec.Command("infocmp", "-A", filepath.Join(home, ".terminfo"), Name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("infocmp %s: %v\n%s", Name, err, out)
	}
	if !strings.Contains(string(out), Name) {
		t.Fatalf("infocmp output missing %q:\n%s", Name, out)
	}
}

func TestInstallMissingTic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())

	err := Install()
	if err == nil {
		t.Fatal("Install() error = nil, want missing tic error")
	}
	if !strings.Contains(err.Error(), "`tic`") {
		t.Fatalf("Install() error = %q, want tic guidance", err)
	}
}

func TestInstallMissingHome(t *testing.T) {
	// Not parallel: overrides the package-level userHomeDir test stub.
	prev := userHomeDir
	userHomeDir = func() (string, error) { return "", nil }
	t.Cleanup(func() { userHomeDir = prev })

	err := Install()
	if err == nil {
		t.Fatal("Install() error = nil, want empty HOME error")
	}
	if !strings.Contains(err.Error(), "HOME is empty") {
		t.Fatalf("Install() error = %q, want empty-HOME guidance", err)
	}
}

func TestInstallDirCreationFailure(t *testing.T) {
	homeFile := filepath.Join(t.TempDir(), "home-file")
	if err := os.WriteFile(homeFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing fake HOME file: %v", err)
	}
	t.Setenv("HOME", homeFile)

	err := Install()
	if err == nil {
		t.Fatal("Install() error = nil, want mkdir failure")
	}
	if !strings.Contains(err.Error(), "creating amux terminfo dir") {
		t.Fatalf("Install() error = %q, want mkdir failure", err)
	}
}

func TestInstallLockFileFailure(t *testing.T) {
	home := t.TempDir()
	installDir := filepath.Join(home, ".terminfo")
	if err := os.MkdirAll(installDir, 0o555); err != nil {
		t.Fatalf("creating read-only install dir: %v", err)
	}
	t.Setenv("HOME", home)

	err := Install()
	if err == nil {
		t.Fatal("Install() error = nil, want lock-file failure")
	}
	if !strings.Contains(err.Error(), "opening amux terminfo lock") {
		t.Fatalf("Install() error = %q, want lock-file failure", err)
	}
}

func TestInstallCreateTempFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))

	err := Install()
	if err == nil {
		t.Fatal("Install() error = nil, want temp-file failure")
	}
	if !strings.Contains(err.Error(), "creating temp terminfo source") {
		t.Fatalf("Install() error = %q, want temp-file failure", err)
	}
}

func TestInstallCompileFailureWithMessage(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)

	ticPath := filepath.Join(binDir, "tic")
	script := "#!/bin/sh\necho compile failed >&2\nexit 1\n"
	if err := os.WriteFile(ticPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake tic: %v", err)
	}

	err := Install()
	if err == nil {
		t.Fatal("Install() error = nil, want compile failure")
	}
	if !strings.Contains(err.Error(), "compile failed") {
		t.Fatalf("Install() error = %q, want fake tic stderr", err)
	}
}

func TestInstallCompileFailureWithoutMessage(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)

	ticPath := filepath.Join(binDir, "tic")
	script := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(ticPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake tic: %v", err)
	}

	err := Install()
	if err == nil {
		t.Fatal("Install() error = nil, want compile failure")
	}
	if !strings.Contains(err.Error(), "compiling amux terminfo") {
		t.Fatalf("Install() error = %q, want compile failure prefix", err)
	}
}

func TestInstallFlockFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	prev := flockFile
	flockFile = func(_ *os.File, how int) error {
		if how == syscall.LOCK_EX {
			return errors.New("lock failed")
		}
		return nil
	}
	t.Cleanup(func() { flockFile = prev })

	err := Install()
	if err == nil {
		t.Fatal("Install() error = nil, want flock failure")
	}
	if !strings.Contains(err.Error(), "locking amux terminfo install") {
		t.Fatalf("Install() error = %q, want flock failure", err)
	}
}

func TestInstallWriteSourceFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	prev := writeTempSource
	writeTempSource = func(_ *os.File, _ string) error { return errors.New("write failed") }
	t.Cleanup(func() { writeTempSource = prev })

	err := Install()
	if err == nil {
		t.Fatal("Install() error = nil, want write failure")
	}
	if !strings.Contains(err.Error(), "writing temp terminfo source") {
		t.Fatalf("Install() error = %q, want write failure", err)
	}
}

func TestInstallCloseSourceFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	prev := closeTempSource
	closeTempSource = func(_ *os.File) error { return errors.New("close failed") }
	t.Cleanup(func() { closeTempSource = prev })

	err := Install()
	if err == nil {
		t.Fatal("Install() error = nil, want close failure")
	}
	if !strings.Contains(err.Error(), "closing temp terminfo source") {
		t.Fatalf("Install() error = %q, want close failure", err)
	}
}
