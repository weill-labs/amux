package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

func TestPprofInfoEndpointReportsRuntimeInfo(t *testing.T) {
	t.Parallel()

	session := fmt.Sprintf("pprof-info-%d", time.Now().UnixNano())
	srv, err := newServerWithScrollbackLogger(session, mux.DefaultScrollbackLines, nil)
	if err != nil {
		t.Fatalf("newServerWithScrollbackLogger: %v", err)
	}
	defer srv.Shutdown()
	if err := srv.EnablePprof(); err != nil {
		t.Fatalf("EnablePprof: %v", err)
	}

	body := fetchServerUnixHTTP(t, PprofSocketPath(session), "/debug/amux/info", time.Second)
	var info struct {
		PID        int    `json:"pid"`
		Uptime     string `json:"uptime"`
		Binary     string `json:"binary"`
		Build      string `json:"build"`
		GoVersion  string `json:"go_version"`
		Goroutines int    `json:"goroutines"`
	}
	if err := json.Unmarshal([]byte(body), &info); err != nil {
		t.Fatalf("Unmarshal info: %v\n%s", err, body)
	}
	if info.PID != os.Getpid() {
		t.Fatalf("pid = %d, want %d", info.PID, os.Getpid())
	}
	if info.Uptime == "" {
		t.Fatal("uptime is empty")
	}
	if info.Binary == "" {
		t.Fatal("binary is empty")
	}
	if info.Build == "" {
		t.Fatal("build is empty")
	}
	if !strings.HasPrefix(info.GoVersion, "go") {
		t.Fatalf("go_version = %q, want go*", info.GoVersion)
	}
	if info.Goroutines < 1 {
		t.Fatalf("goroutines = %d, want > 0", info.Goroutines)
	}
}

func TestPprofEndpointRespondsWhileSessionMutationPathWedged(t *testing.T) {
	t.Parallel()

	session := fmt.Sprintf("pprof-wedge-%d", time.Now().UnixNano())
	srv, err := newServerWithScrollbackLogger(session, mux.DefaultScrollbackLines, nil)
	if err != nil {
		t.Fatalf("newServerWithScrollbackLogger: %v", err)
	}
	defer srv.Shutdown()
	if err := srv.EnablePprof(); err != nil {
		t.Fatalf("EnablePprof: %v", err)
	}
	sess := srv.firstSession()
	if sess == nil {
		t.Fatal("server has no session")
	}

	wedgeStarted := make(chan struct{})
	releaseWedge := make(chan struct{})
	wedgedDone := make(chan commandMutationResult, 1)
	go func() {
		wedgedDone <- sess.enqueueCommandMutation(func(*MutationContext) commandMutationResult {
			close(wedgeStarted)
			<-releaseWedge
			return commandMutationResult{output: "released\n"}
		})
	}()

	select {
	case <-wedgeStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for wedged mutation to start")
	}

	normalDone := make(chan commandMutationResult, 1)
	go func() {
		normalDone <- sess.enqueueCommandMutation(func(*MutationContext) commandMutationResult {
			return commandMutationResult{output: "normal\n"}
		})
	}()

	select {
	case res := <-normalDone:
		t.Fatalf("normal mutation completed while wedge was active: %+v", res)
	case <-time.After(100 * time.Millisecond):
	}

	body := fetchServerUnixHTTP(t, PprofSocketPath(session), "/debug/pprof/goroutine?debug=1", time.Second)
	if !strings.Contains(body, "goroutine profile:") {
		t.Fatalf("goroutine profile missing header:\n%s", body)
	}

	close(releaseWedge)
	select {
	case res := <-wedgedDone:
		if res.err != nil {
			t.Fatalf("wedged mutation error after release: %v", res.err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for wedged mutation to release")
	}
	select {
	case res := <-normalDone:
		if res.err != nil {
			t.Fatalf("normal mutation error after release: %v", res.err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for normal mutation after release")
	}
}

func fetchServerUnixHTTP(t *testing.T, sockPath, path string, timeout time.Duration) string {
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(%s): %v", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %s, want 200 OK\nbody:\n%s", path, resp.Status, body)
	}
	return string(body)
}
