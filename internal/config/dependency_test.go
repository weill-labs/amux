package config

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestConfigPackageDoesNotImportMux(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))

	cmd := exec.Command("go", "list", "-f", "{{join .Imports \"\\n\"}}", "./internal/config")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list ./internal/config: %v\n%s", err, out)
	}

	if strings.Contains(string(out), "github.com/weill-labs/amux/internal/mux") {
		t.Fatalf("internal/config imports internal/mux:\n%s", out)
	}
}
