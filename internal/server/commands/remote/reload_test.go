package remote

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRequestedReloadExecPathNormalizesProvidedPath(t *testing.T) {
	t.Parallel()

	execPath := filepath.Join(t.TempDir(), "amux")
	if err := os.WriteFile(execPath, []byte("placeholder"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", execPath, err)
	}

	got, err := RequestedReloadExecPath([]string{ReloadServerExecPathFlag, execPath})
	if err != nil {
		t.Fatalf("RequestedReloadExecPath() error = %v", err)
	}
	if got != execPath {
		t.Fatalf("RequestedReloadExecPath() = %q, want %q", got, execPath)
	}
}

func TestRequestedReloadExecPathRejectsMissingValue(t *testing.T) {
	t.Parallel()

	if _, err := RequestedReloadExecPath([]string{ReloadServerExecPathFlag}); err == nil {
		t.Fatal("RequestedReloadExecPath() should fail without a value")
	}
}

func TestRequestedReloadExecPathRejectsMissingBinary(t *testing.T) {
	t.Parallel()

	missingPath := filepath.Join(t.TempDir(), "missing-amux")
	if _, err := RequestedReloadExecPath([]string{ReloadServerExecPathFlag, missingPath}); err == nil {
		t.Fatalf("RequestedReloadExecPath(%q) should fail", missingPath)
	}
}
