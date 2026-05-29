package test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/checkpoint"
)

func TestVersionCommand(t *testing.T) {
	t.Parallel()
	out, err := newHermeticAmuxCommand(t, "version").CombinedOutput()
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

func TestVersionFlagAliases(t *testing.T) {
	want, err := newHermeticAmuxCommand(t, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("amux version failed: %v\n%s", err, want)
	}

	for _, flag := range []string{"--version", "-V"} {
		flag := flag
		t.Run(flag, func(t *testing.T) {
			t.Parallel()

			out, err := newHermeticAmuxCommand(t, flag).CombinedOutput()
			if err != nil {
				t.Fatalf("amux %s failed: %v\n%s", flag, err, out)
			}
			if string(out) != string(want) {
				t.Fatalf("amux %s output = %q, want %q", flag, out, want)
			}
		})
	}
}

func TestTmuxStyleUnknownCommandHints(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"capture-pane", "pipe-pane", "pane"} {
		command := command
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			out, err := newHermeticAmuxCommand(t, command).CombinedOutput()
			if err == nil {
				t.Fatalf("amux %s succeeded, want non-zero exit\n%s", command, out)
			}
			output := string(out)
			if !strings.Contains(output, "amux: unknown command \""+command+"\"") {
				t.Fatalf("amux %s output missing unknown command error:\n%s", command, output)
			}
			if !strings.Contains(output, "did you mean `amux capture`?") {
				t.Fatalf("amux %s output missing capture hint:\n%s", command, output)
			}
		})
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
