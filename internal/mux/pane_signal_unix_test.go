//go:build darwin || linux

package mux

import (
	"os"
	"testing"
)

func TestForegroundProcessGroupNilPTMX(t *testing.T) {
	t.Parallel()

	pgrp, err := (&Pane{}).foregroundProcessGroup()
	if err != nil || pgrp != 0 {
		t.Fatalf("foregroundProcessGroup() with nil ptmx = (%d, %v), want (0, nil)", pgrp, err)
	}
}

func TestForegroundProcessGroupNonTTYReturnsError(t *testing.T) {
	t.Parallel()

	// A regular file is not a terminal, so TIOCGPGRP fails with ENOTTY. This
	// exercises the error path without depending on a live PTY.
	f, err := os.CreateTemp(t.TempDir(), "not-a-tty")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	pgrp, err := (&Pane{ptmx: f}).foregroundProcessGroup()
	if err == nil {
		t.Fatalf("foregroundProcessGroup() on non-tty = (%d, nil), want error", pgrp)
	}
	if pgrp != 0 {
		t.Fatalf("foregroundProcessGroup() on non-tty pgrp = %d, want 0", pgrp)
	}
}
