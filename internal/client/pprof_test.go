package client

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestPprofEndpointServesAndPromotesLatestClientAlias(t *testing.T) {
	t.Parallel()

	session := "client-pprof-" + strings.ReplaceAll(t.Name(), "/", "-")
	aliasPath := PprofSocketPath(session)
	_ = os.Remove(aliasPath)
	t.Cleanup(func() { _ = os.Remove(aliasPath) })

	endpoint1, err := newPprofEndpoint(session, 1111)
	if err != nil {
		t.Fatalf("newPprofEndpoint(first): %v", err)
	}
	defer endpoint1.close()

	if got, err := os.Readlink(aliasPath); err != nil {
		t.Fatalf("Readlink(%q): %v", aliasPath, err)
	} else if got != PprofProcessSocketPath(session, 1111) {
		t.Fatalf("alias target = %q, want %q", got, PprofProcessSocketPath(session, 1111))
	}

	body := fetchUnixHTTP(t, aliasPath, "/debug/pprof/goroutine?debug=2", 5*time.Second)
	if !strings.Contains(body, "goroutine") {
		t.Fatalf("goroutine dump missing goroutine text:\n%s", body)
	}

	endpoint2, err := newPprofEndpoint(session, 2222)
	if err != nil {
		t.Fatalf("newPprofEndpoint(second): %v", err)
	}

	if got, err := os.Readlink(aliasPath); err != nil {
		t.Fatalf("Readlink(%q): %v", aliasPath, err)
	} else if got != PprofProcessSocketPath(session, 2222) {
		t.Fatalf("alias target = %q, want %q", got, PprofProcessSocketPath(session, 2222))
	}

	endpoint2.close()
	if got, err := os.Readlink(aliasPath); err != nil {
		t.Fatalf("Readlink(%q) after fallback: %v", aliasPath, err)
	} else if got != PprofProcessSocketPath(session, 1111) {
		t.Fatalf("fallback alias target = %q, want %q", got, PprofProcessSocketPath(session, 1111))
	}

	endpoint1.close()
	if _, err := os.Lstat(aliasPath); !os.IsNotExist(err) {
		t.Fatalf("Lstat(%q) after close = %v, want not exist", aliasPath, err)
	}
}

func TestNewPprofEndpointRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	if _, err := newPprofEndpoint("", 1); err == nil || !strings.Contains(err.Error(), "active session") {
		t.Fatalf("newPprofEndpoint(empty session) error = %v, want active-session error", err)
	}
	if _, err := newPprofEndpoint("client-pprof-invalid", 0); err == nil || !strings.Contains(err.Error(), "valid pid") {
		t.Fatalf("newPprofEndpoint(invalid pid) error = %v, want valid-pid error", err)
	}
}

func TestNewPprofEndpointRejectsLiveExistingSocket(t *testing.T) {
	t.Parallel()

	session := fmt.Sprintf("cppl-%d", time.Now().UnixNano())
	sockPath := PprofProcessSocketPath(session, 3333)
	if err := os.MkdirAll(filepath.Dir(sockPath), 0700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(sockPath), err)
	}
	_ = os.Remove(sockPath)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Listen(%q): %v", sockPath, err)
	}
	t.Cleanup(func() {
		_ = ln.Close()
		_ = os.Remove(sockPath)
	})

	if _, err := newPprofEndpoint(session, 3333); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("newPprofEndpoint(existing socket) error = %v, want already-running error", err)
	}
}

func TestNewPprofEndpointFailsWhenAliasCannotBePublished(t *testing.T) {
	t.Parallel()

	session := fmt.Sprintf("cppa-%d", time.Now().UnixNano())
	aliasPath := PprofSocketPath(session)
	_ = os.Remove(aliasPath)
	if err := os.MkdirAll(aliasPath, 0700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", aliasPath, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(aliasPath) })

	if _, err := newPprofEndpoint(session, 4444); err == nil || !strings.Contains(err.Error(), "publishing pprof socket alias") {
		t.Fatalf("newPprofEndpoint(alias dir) error = %v, want alias publish error", err)
	}
	if _, err := os.Stat(PprofProcessSocketPath(session, 4444)); !os.IsNotExist(err) {
		t.Fatalf("process socket should be removed after alias publish failure, stat err = %v", err)
	}
}

func TestPromoteFallbackPprofAliasRemovesDeadSockets(t *testing.T) {
	t.Parallel()

	session := fmt.Sprintf("cppf-%d", time.Now().UnixNano())
	aliasPath := PprofSocketPath(session)
	_ = os.Remove(aliasPath)
	t.Cleanup(func() { _ = os.Remove(aliasPath) })

	live, err := newPprofEndpoint(session, 5555)
	if err != nil {
		t.Fatalf("newPprofEndpoint(live): %v", err)
	}
	defer live.close()

	stalePath := PprofProcessSocketPath(session, 6666)
	_ = os.Remove(stalePath)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: stalePath, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix(%q): %v", stalePath, err)
	}
	ln.SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatalf("Close(%q): %v", stalePath, err)
	}

	promoteFallbackPprofAlias(session, aliasPath, live.sockPath)
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("stale socket should be removed, stat err = %v", err)
	}
	if _, err := os.Lstat(aliasPath); !os.IsNotExist(err) {
		t.Fatalf("alias should be removed when only stale candidates remain, stat err = %v", err)
	}
}
