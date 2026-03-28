package mux

import (
	"errors"
	"os/exec"
	"testing"
	"time"
)

func TestProcessCommandOutputReturnsStdout(t *testing.T) {
	t.Parallel()

	out, err := processCommandOutput("sh", "-c", "printf ok")
	if err != nil {
		t.Fatalf("processCommandOutput() error = %v, want nil", err)
	}
	if got := string(out); got != "ok" {
		t.Fatalf("processCommandOutput() = %q, want %q", got, "ok")
	}
}

func TestProcessCommandOutputTimesOutWhenDescendantKeepsPipeOpen(t *testing.T) {
	t.Parallel()

	start := time.Now()
	_, err := processCommandOutput("sh", "-c", "sleep 30 & printf ok")
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("processCommandOutput() error = %v, want %v", err, exec.ErrWaitDelay)
	}
	if elapsed := time.Since(start); elapsed > 2*processTimeout {
		t.Fatalf("processCommandOutput() took %v, want <= %v", elapsed, 2*processTimeout)
	}
}
