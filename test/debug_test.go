package test

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/client"
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

func TestDebugClientGoroutinesCommandReportsDisabledEndpoint(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := h.commandWithContext(ctx, "debug", "client-goroutines")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("debug client-goroutines should fail when pprof is disabled.\nOutput:\n%s", out)
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

func TestDebugClientCommandsUseInteractiveClientPprofEndpoint(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	writeDebugPprofConfig(t, h)

	pty := newPTYClientHarness(t, h)
	if _, err := os.Lstat(client.PprofSocketPath(h.session)); err != nil {
		t.Fatalf("Lstat(%q): %v", client.PprofSocketPath(h.session), err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	goroutinesCmd := h.commandWithContext(ctx, "debug", "client-goroutines")
	goroutinesOut, err := goroutinesCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("debug client-goroutines: %v\n%s", err, goroutinesOut)
	}
	if !strings.Contains(string(goroutinesOut), "goroutine") {
		t.Fatalf("goroutine dump missing goroutine text:\n%s", goroutinesOut)
	}

	ctxHeap, cancelHeap := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelHeap()
	heapCmd := h.commandWithContext(ctxHeap, "debug", "client-heap")
	heapOut, err := heapCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("debug client-heap: %v\n%s", err, heapOut)
	}
	if !strings.Contains(string(heapOut), "heap profile") {
		t.Fatalf("heap output missing heap profile text:\n%s", heapOut)
	}

	pty.detach()
	if !pty.waitExited(5 * time.Second) {
		t.Fatal("interactive client did not exit after detach")
	}
}

func TestDebugClientProfileUsesNewestClientAndPrunesStaleSocket(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	writeDebugPprofConfig(t, h)

	stalePath := client.PprofProcessSocketPath(h.session, 999999)
	leaveStaleUnixSocket(t, stalePath)

	aliasPath := client.PprofSocketPath(h.session)
	_ = os.Remove(aliasPath)
	if err := os.Symlink(stalePath, aliasPath); err != nil {
		t.Fatalf("Symlink(%q, %q): %v", stalePath, aliasPath, err)
	}
	t.Cleanup(func() { _ = os.Remove(aliasPath) })

	ptyA := newPTYClientHarness(t, h)
	ptyB := newPTYClientHarness(t, h)
	clientASocket := client.PprofProcessSocketPath(h.session, ptyA.cmd.Process.Pid)
	clientBSocket := client.PprofProcessSocketPath(h.session, ptyB.cmd.Process.Pid)

	ptyA.detach()
	if !ptyA.waitExited(5 * time.Second) {
		t.Fatal("first interactive client did not exit after detach")
	}
	if _, err := os.Stat(clientASocket); !os.IsNotExist(err) {
		t.Fatalf("first client socket should be removed after detach, stat err = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	profileCmd := h.commandWithContext(ctx, "debug", "client-profile", "--duration", "1s")
	profileOut, err := profileCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("debug client-profile: %v\n%s", err, profileOut)
	}
	if len(profileOut) < 2 || profileOut[0] != 0x1f || profileOut[1] != 0x8b {
		t.Fatalf("client profile output should start with gzip magic, got % x", profileOut[:min(8, len(profileOut))])
	}
	if got, err := os.Readlink(aliasPath); err != nil {
		t.Fatalf("Readlink(%q): %v", aliasPath, err)
	} else if got != clientBSocket {
		t.Fatalf("client pprof alias = %q, want newest client socket %q", got, clientBSocket)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("stale client socket should be pruned on client startup, stat err = %v", err)
	}

	ptyB.detach()
	if !ptyB.waitExited(5 * time.Second) {
		t.Fatal("second interactive client did not exit after detach")
	}
}

func writeDebugPprofConfig(t *testing.T, h *ServerHarness) {
	t.Helper()

	configPath := filepath.Join(h.home, ".config", "amux", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(configPath), err)
	}
	if err := os.WriteFile(configPath, []byte("[debug]\npprof = true\n"), 0644); err != nil {
		t.Fatalf("WriteFile(%q): %v", configPath, err)
	}
}

func leaveStaleUnixSocket(t *testing.T, sockPath string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(sockPath), 0700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(sockPath), err)
	}
	_ = os.Remove(sockPath)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix(%q): %v", sockPath, err)
	}
	ln.SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatalf("Close(%q): %v", sockPath, err)
	}
	t.Cleanup(func() { _ = os.Remove(sockPath) })
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
	cmd := h.commandWithContext(ctx, "debug", "goroutines")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("amux -s %s debug goroutines: %v\n%s", h.session, err, out)
	}
	if !strings.Contains(string(out), "goroutine") {
		t.Fatalf("goroutine dump missing goroutine text:\n%s", out)
	}
}
