package test

import (
	"context"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/server"
)

func fetchUnixHTTP(t *testing.T, sockPath, path string, timeout time.Duration) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sockPath)
		},
	}
	t.Cleanup(transport.CloseIdleConnections)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://amux"+path, nil)
	if err != nil {
		t.Fatalf("NewRequest(%q): %v", path, err)
	}

	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		t.Fatalf("GET %s over %s: %v", path, sockPath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s status = %s, want 200 OK\nbody:\n%s", path, resp.Status, body)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(%s): %v", path, err)
	}
	return string(body)
}

func TestDebugEndpointServesGoroutineDump(t *testing.T) {
	t.Parallel()

	h := newServerHarnessWithConfig(t, 80, 24, "[debug]\npprof = true\n")
	sockPath := filepath.Join(server.SocketDir(), h.session+".pprof")

	body := fetchUnixHTTP(t, sockPath, "/debug/pprof/goroutine?debug=2", 5*time.Second)
	if !strings.Contains(body, "goroutine") {
		t.Fatalf("goroutine dump missing goroutine text:\n%s", body)
	}
	if out := h.runCmd("status"); !strings.Contains(out, "windows:") {
		t.Fatalf("server should stay alive after goroutine dump.\nStatus:\n%s", out)
	}
}

func TestDebugGoroutinesCommandReportsDisabledEndpoint(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := h.commandWithContext(ctx, "debug", "goroutines")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("debug goroutines should fail when pprof is disabled.\nOutput:\n%s", out)
	}
	if !strings.Contains(string(out), "pprof debug endpoint is disabled") {
		t.Fatalf("output = %q, want disabled-endpoint error", out)
	}
}

func TestDebugGoroutinesCommandPrintsDump(t *testing.T) {
	t.Parallel()

	h := newServerHarnessWithConfig(t, 80, 24, "[debug]\npprof = true\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := h.commandWithContext(ctx, "debug", "goroutines")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("debug goroutines: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "goroutine") {
		t.Fatalf("goroutine dump missing goroutine text:\n%s", out)
	}
}

func TestDebugProfileCommandStreamsCPUProfile(t *testing.T) {
	t.Parallel()

	h := newServerHarnessWithConfig(t, 80, 24, "[debug]\npprof = true\n")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := h.commandWithContext(ctx, "debug", "profile", "--duration", "1s")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("debug profile: %v\n%s", err, out)
	}
	if len(out) < 2 || out[0] != 0x1f || out[1] != 0x8b {
		t.Fatalf("profile output should start with gzip magic, got % x", out[:min(8, len(out))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestDebugCommandUsesServerSession(t *testing.T) {
	t.Parallel()

	h := newServerHarnessWithConfig(t, 80, 24, "[debug]\npprof = true\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, amuxBin, "-s", h.session, "debug", "goroutines")
	cmd.Env = h.commandWithContext(ctx).Env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("amux -s %s debug goroutines: %v\n%s", h.session, err, out)
	}
	if !strings.Contains(string(out), "goroutine") {
		t.Fatalf("goroutine dump missing goroutine text:\n%s", out)
	}
}
