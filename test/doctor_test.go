package test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/server"
)

func TestDoctorHealthySessionExitsOK(t *testing.T) {
	t.Parallel()

	h := newServerHarnessWithConfig(t, 80, 24, "[debug]\npprof = true\n")
	writeDebugPprofConfig(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := h.commandWithContext(ctx, "doctor")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("doctor healthy session: %v\n%s", err, out)
	}

	output := string(out)
	for _, want := range []string{
		"amux doctor",
		"overall: ok",
		"[ok] config",
		"[ok] pprof",
		"[ok] server-reachable",
		"[ok] goroutines",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, output)
		}
	}
}

func TestDoctorWarnsWhenPprofDisabled(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := h.commandWithContext(ctx, "doctor")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("doctor with pprof disabled exited 0, want warning exit\n%s", out)
	}
	exitErr := mustExitError(t, err, out)
	if exitErr.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitErr.ExitCode(), out)
	}

	output := string(out)
	for _, want := range []string{
		"overall: warn",
		"[warn] pprof",
		"Set `[debug] pprof = true`",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, output)
		}
	}
}

func TestDoctorFailsWhenServerSocketIsWedged(t *testing.T) {
	t.Parallel()

	session := fmt.Sprintf("doctor-wedged-%d", time.Now().UnixNano())
	sockPath := server.SocketPath(session)
	wedgedUnixSocket(t, sockPath)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := newHermeticAmuxCommandContext(t, ctx, "-s", session, "doctor")
	cmd.Env = append(cmd.Env, "AMUX_DIAL_TIMEOUT=20ms", "AMUX_SOCKET_DIR="+testSocketDir())
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("doctor against wedged socket exited 0, want failure\n%s", out)
	}
	exitErr := mustExitError(t, err, out)
	if exitErr.ExitCode() != 2 {
		t.Fatalf("exit code = %d, want 2\n%s", exitErr.ExitCode(), out)
	}

	output := string(out)
	for _, want := range []string{
		"overall: fail",
		"[fail] server-reachable",
		"Server unresponsive",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, output)
		}
	}
}

func TestDoctorJSONMatchesCheckedInSchema(t *testing.T) {
	t.Parallel()

	h := newServerHarnessWithConfig(t, 80, 24, "[debug]\npprof = true\n")
	writeDebugPprofConfig(t, h)

	schemaPath := repoPath(t, "test/testdata/doctor.schema.json")
	schemaData, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", schemaPath, err)
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		t.Fatalf("doctor schema is not valid JSON: %v\n%s", err, schemaData)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := h.commandWithContext(ctx, "doctor", "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("doctor --json: %v\n%s", err, out)
	}

	var report struct {
		SchemaVersion int    `json:"schema_version"`
		Overall       string `json:"overall"`
		ExitCode      int    `json:"exit_code"`
		Session       string `json:"session"`
		Checks        []struct {
			Name    string `json:"name"`
			Scope   string `json:"scope"`
			Session string `json:"session,omitempty"`
			Status  string `json:"status"`
			Summary string `json:"summary"`
			Hint    string `json:"hint,omitempty"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("doctor --json output is invalid JSON: %v\n%s", err, out)
	}

	assertDoctorReportMatchesSchemaContract(t, schema, report.SchemaVersion, report.Overall, report.ExitCode, report.Session, len(report.Checks))
	requireDoctorJSONCheck(t, report.Checks, "server-reachable", "ok")
	requireDoctorJSONCheck(t, report.Checks, "goroutines", "ok")
}

func assertDoctorReportMatchesSchemaContract(t *testing.T, schema map[string]any, schemaVersion int, overall string, exitCode int, session string, checkCount int) {
	t.Helper()

	if schema["$schema"] == "" {
		t.Fatal("doctor schema missing $schema")
	}
	if schemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", schemaVersion)
	}
	if overall != "ok" {
		t.Fatalf("overall = %q, want ok", overall)
	}
	if exitCode != 0 {
		t.Fatalf("exit_code = %d, want 0", exitCode)
	}
	if session == "" {
		t.Fatal("session = empty, want resolved session")
	}
	if checkCount == 0 {
		t.Fatal("checks = empty, want doctor check results")
	}
}

func requireDoctorJSONCheck(t *testing.T, checks any, name, status string) {
	t.Helper()

	typed, ok := checks.([]struct {
		Name    string `json:"name"`
		Scope   string `json:"scope"`
		Session string `json:"session,omitempty"`
		Status  string `json:"status"`
		Summary string `json:"summary"`
		Hint    string `json:"hint,omitempty"`
	})
	if !ok {
		t.Fatalf("unexpected checks type %T", checks)
	}
	for _, check := range typed {
		if check.Name == name {
			if check.Status != status {
				t.Fatalf("check %q status = %q, want %q", name, check.Status, status)
			}
			if check.Scope == "" {
				t.Fatalf("check %q scope = empty", name)
			}
			if check.Summary == "" {
				t.Fatalf("check %q summary = empty", name)
			}
			return
		}
	}
	t.Fatalf("missing doctor check %q in %#v", name, typed)
}

func wedgedUnixSocket(t *testing.T, sockPath string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(sockPath), err)
	}
	_ = os.Remove(sockPath)

	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("Socket(AF_UNIX): %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Close(fd)
		_ = os.Remove(sockPath)
	})

	if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: sockPath}); err != nil {
		t.Fatalf("Bind(%q): %v", sockPath, err)
	}
	if err := syscall.Listen(fd, 0); err != nil {
		t.Fatalf("Listen(%q): %v", sockPath, err)
	}

	var fillConns []net.Conn
	t.Cleanup(func() {
		for _, conn := range fillConns {
			_ = conn.Close()
		}
	})
	for attempts := 0; attempts < 64; attempts++ {
		conn, err := net.DialTimeout("unix", sockPath, 5*time.Millisecond)
		if err != nil {
			if !isTimeoutNetError(err) && !errors.Is(err, syscall.EAGAIN) {
				t.Fatalf("filling wedged socket queue: %v", err)
			}
			return
		}
		fillConns = append(fillConns, conn)
	}
	t.Fatal("wedged socket accept queue did not fill")
}
