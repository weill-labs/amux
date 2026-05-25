package test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDiagDumpCommandPrintsGoroutineDump(t *testing.T) {
	t.Parallel()

	h := newServerHarnessWithConfig(t, 80, 24, "[debug]\npprof = true\n")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := h.commandWithContext(ctx, "_diag", "dump")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("_diag dump: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "goroutine") {
		t.Fatalf("goroutine dump missing goroutine text:\n%s", out)
	}
}

func TestDiagGoroutinesCommandPrintsHistogram(t *testing.T) {
	t.Parallel()

	h := newServerHarnessWithConfig(t, 80, 24, "[debug]\npprof = true\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := h.commandWithContext(ctx, "_diag", "goroutines")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("_diag goroutines: %v\n%s", err, out)
	}
	for _, want := range []string{"goroutines:", "states:"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("_diag goroutines output missing %q:\n%s", want, out)
		}
	}
}

func TestDiagCommandReportsDisabledEndpoint(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := h.commandWithContext(ctx, "_diag", "dump")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("_diag dump should fail when pprof is disabled.\nOutput:\n%s", out)
	}
	for _, want := range []string{"[debug] pprof = true", "~/.config/amux/config.toml", "restart"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("output = %q, want substring %q", out, want)
		}
	}
}

func TestDiagInfoIncludesWatchdogLogLines(t *testing.T) {
	t.Parallel()

	h := newServerHarnessWithConfig(t, 80, 24, "[debug]\npprof = true\n")
	watchdogLine := `{"event":"event_loop_watchdog","session":"` + h.session + `","elapsed":"31s"}`
	if err := appendFile(h.logPath, "\n"+watchdogLine+"\n"); err != nil {
		t.Fatalf("append watchdog line: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := h.commandWithContext(ctx, "_diag", "info")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("_diag info: %v\n%s", err, out)
	}
	for _, want := range []string{"pid:", "uptime:", "binary:", "build:", "go:", "goroutines:", "recent event_loop_watchdog log lines:", watchdogLine} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("_diag info output missing %q:\n%s", want, out)
		}
	}
}

func appendFile(path, data string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(data)
	return err
}
