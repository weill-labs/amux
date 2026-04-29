package dialutil

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDialUnixHelpersTimeoutOnWedgedSocket(t *testing.T) {
	t.Parallel()

	sockPath := newWedgedUnixSocket(t)
	timeout := 40 * time.Millisecond

	tests := []struct {
		name    string
		timeout time.Duration
		dial    func(context.Context, string) (net.Conn, error)
	}{
		{
			name:    "dial",
			timeout: timeout,
			dial: func(_ context.Context, path string) (net.Conn, error) {
				return DialUnixWithDefault(path, timeout)
			},
		},
		{
			name:    "dial context",
			timeout: timeout,
			dial: func(ctx context.Context, path string) (net.Conn, error) {
				return DialUnixContextWithDefault(ctx, path, timeout)
			},
		},
		{
			name:    "stale probe",
			timeout: StaleProbeDialTimeout,
			dial: func(_ context.Context, path string) (net.Conn, error) {
				return DialUnixStaleProbe(path)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			start := time.Now()
			conn, err := tt.dial(context.Background(), sockPath)
			elapsed := time.Since(start)
			if conn != nil {
				conn.Close()
			}
			if !isTimeoutError(err) {
				t.Fatalf("dial error = %v, want timeout", err)
			}
			if elapsed > tt.timeout+220*time.Millisecond {
				t.Fatalf("dial returned in %v, want within configured timeout %v", elapsed, tt.timeout)
			}
		})
	}
}

func TestDialTimeoutDefaults(t *testing.T) {
	t.Parallel()

	if DefaultDialTimeout != 2*time.Second {
		t.Fatalf("DefaultDialTimeout = %v, want 2s", DefaultDialTimeout)
	}
	if StaleProbeDialTimeout != 500*time.Millisecond {
		t.Fatalf("StaleProbeDialTimeout = %v, want 500ms", StaleProbeDialTimeout)
	}
}

func TestDialUnixMissingSocketReturnsBeforeTimeout(t *testing.T) {
	t.Parallel()

	missingPath := filepath.Join(t.TempDir(), "missing.sock")
	timeout := 80 * time.Millisecond

	start := time.Now()
	conn, err := DialUnixWithDefault(missingPath, timeout)
	elapsed := time.Since(start)
	if conn != nil {
		conn.Close()
	}
	if err == nil {
		t.Fatal("DialUnixWithDefault() error = nil, want missing-socket error")
	}
	if isTimeoutError(err) {
		t.Fatalf("DialUnixWithDefault() error = %v, want non-timeout missing-socket error", err)
	}
	if elapsed > timeout {
		t.Fatalf("DialUnixWithDefault() returned in %v, want before timeout %v", elapsed, timeout)
	}
}

func TestDialTimeoutFromEnvOverridesDefault(t *testing.T) {
	t.Setenv(EnvDialTimeout, "500ms")
	sockPath := newWedgedUnixSocket(t)

	if got := TimeoutFromEnv(DefaultDialTimeout); got != 500*time.Millisecond {
		t.Fatalf("TimeoutFromEnv() = %v, want env override", got)
	}

	start := time.Now()
	conn, err := DialUnix(sockPath)
	elapsed := time.Since(start)
	if conn != nil {
		conn.Close()
	}
	if !isTimeoutError(err) {
		t.Fatalf("DialUnix() error = %v, want timeout", err)
	}
	if elapsed > 700*time.Millisecond {
		t.Fatalf("DialUnix() returned in %v, want within env timeout budget", elapsed)
	}
}

func TestDialTimeoutFromEnvWarnsAndUsesDefaultOnParseError(t *testing.T) {
	t.Setenv(EnvDialTimeout, "garbage")
	var warnings []string

	got := TimeoutFromEnvWithLogger(75*time.Millisecond, func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	})

	if got != 75*time.Millisecond {
		t.Fatalf("TimeoutFromEnvWithLogger() = %v, want default", got)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(warnings))
	}
	if !strings.Contains(warnings[0], EnvDialTimeout) || !strings.Contains(warnings[0], "garbage") {
		t.Fatalf("warning = %q, want env var name and invalid value", warnings[0])
	}
}

func newWedgedUnixSocket(t *testing.T) string {
	t.Helper()

	sockPath := filepath.Join(t.TempDir(), "wedged.sock")
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
			if !isTimeoutError(err) {
				t.Fatalf("filling wedged socket queue: %v", err)
			}
			return sockPath
		}
		fillConns = append(fillConns, conn)
	}
	t.Fatal("wedged socket accept queue did not fill")
	return ""
}

func isTimeoutError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
