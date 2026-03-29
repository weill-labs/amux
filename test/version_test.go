package test

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/checkpoint"
)

func TestVersionCommand(t *testing.T) {
	t.Parallel()
	out, err := exec.Command(amuxBin, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("amux version failed: %v\n%s", err, out)
	}
	output := string(out)
	if !strings.Contains(output, "build:") {
		t.Errorf("version output should contain 'build:', got:\n%s", output)
	}
	if !strings.Contains(output, "checkpoint v"+strconv.Itoa(checkpoint.ServerCheckpointVersion)) {
		t.Errorf("version output should contain checkpoint version, got:\n%s", output)
	}
}

func TestStatusIncludesBuild(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	statusOut := h.runCmd("status")
	if !strings.Contains(statusOut, "build:") {
		t.Errorf("status output should contain server build info, got:\n%s", statusOut)
	}
	if !strings.Contains(statusOut, "checkpoint v"+strconv.Itoa(checkpoint.ServerCheckpointVersion)) {
		t.Errorf("status output should contain checkpoint version, got:\n%s", statusOut)
	}
}
