package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallTerminfoCommand(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	for i := 0; i < 2; i++ {
		cmd := exec.Command(amuxBin, "install-terminfo")
		cmd.Env = upsertEnv(os.Environ(), "HOME", home)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("install-terminfo run %d failed: %v\n%s", i+1, err, out)
		}
	}

	verify := exec.Command("infocmp", "-A", filepath.Join(home, ".terminfo"), "amux")
	out, err := verify.CombinedOutput()
	if err != nil {
		t.Fatalf("infocmp amux failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "amux") {
		t.Fatalf("infocmp output missing amux entry:\n%s", out)
	}
}

func TestInstallTerminfoCommandFailsWithoutTic(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cmd := exec.Command(amuxBin, "install-terminfo")
	cmd.Env = upsertEnv(upsertEnv(os.Environ(), "HOME", home), "PATH", t.TempDir())
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("install-terminfo unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(string(out), "amux install-terminfo:") {
		t.Fatalf("missing install-terminfo failure prefix:\n%s", out)
	}
}

func TestServerBootstrapFailsWithoutTic(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cmd := exec.Command(amuxBin, "_server", "terminfo-fail")
	cmd.Env = upsertEnv(upsertEnv(os.Environ(), "HOME", home), "PATH", t.TempDir())
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("server bootstrap unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(string(out), "amux server:") {
		t.Fatalf("missing server bootstrap failure prefix:\n%s", out)
	}
}
