package server

import (
	"os"
	"slices"
	"testing"
)

func TestReadServerEnvScrubsLogDirAndExportsForReload(t *testing.T) {
	t.Setenv("AMUX_EXIT_UNATTACHED", "1")
	t.Setenv("AMUX_NO_WATCH", "1")
	t.Setenv("AMUX_LOG_DIR", "/tmp/amux-test-logs")

	env := ReadServerEnv()
	if !env.ExitUnattached {
		t.Fatal("ExitUnattached = false, want true")
	}
	if !env.NoWatch {
		t.Fatal("NoWatch = false, want true")
	}
	if env.LogDir != "/tmp/amux-test-logs" {
		t.Fatalf("LogDir = %q, want /tmp/amux-test-logs", env.LogDir)
	}

	for _, key := range []string{"AMUX_EXIT_UNATTACHED", "AMUX_NO_WATCH", "AMUX_LOG_DIR"} {
		if got := os.Getenv(key); got != "" {
			t.Fatalf("%s still exported as %q", key, got)
		}
	}

	exported := env.Export()
	for _, want := range []string{"AMUX_EXIT_UNATTACHED=1", "AMUX_NO_WATCH=1", "AMUX_LOG_DIR=/tmp/amux-test-logs"} {
		if !slices.Contains(exported, want) {
			t.Fatalf("Export() = %v, want %q", exported, want)
		}
	}
}
