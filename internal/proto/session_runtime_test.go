package proto

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSocketDirUsesEnvOverride(t *testing.T) {
	override := filepath.Join(t.TempDir(), "sockets")
	t.Setenv("AMUX_SOCKET_DIR", override)

	if got := SocketDir(); got != override {
		t.Fatalf("SocketDir() = %q, want %q", got, override)
	}
	if got, want := SocketPath("test-session"), filepath.Join(override, "test-session"); got != want {
		t.Fatalf("SocketPath() = %q, want %q", got, want)
	}
}

func TestDefaultSocketDirIgnoresEnvOverride(t *testing.T) {
	override := filepath.Join(t.TempDir(), "sockets")
	t.Setenv(SocketDirEnv, override)

	want := fmt.Sprintf("/tmp/amux-%d", os.Getuid())
	if got := DefaultSocketDir(); got != want {
		t.Fatalf("DefaultSocketDir() = %q, want %q", got, want)
	}
}
