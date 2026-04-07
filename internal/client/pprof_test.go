package client

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
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
