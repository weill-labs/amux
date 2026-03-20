package terminfo

import (
	"os/exec"
	"path/filepath"
	"strings"
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
