package cli

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/server"
)

func TestParseDebugCommand(t *testing.T) {
	t.Parallel()

	sessionName := "debug-test"
	wantSocket := server.PprofSocketPath(sessionName)

	tests := []struct {
		name         string
		loadConfig   debugConfigLoader
		args         []string
		wantPath     string
		wantTimeout  time.Duration
		wantSockPath string
		wantEnabled  bool
		wantErr      string
	}{
		{
			name:       "rejects missing subcommand",
			loadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
			wantErr:    debugUsage,
		},
		{
			name:       "surfaces config load error",
			loadConfig: func() (*config.Config, error) { return nil, errors.New("boom") },
			args:       []string{"goroutines"},
			wantErr:    "loading config: boom",
		},
		{
			name:         "parses goroutines",
			loadConfig:   func() (*config.Config, error) { return &config.Config{Debug: config.DebugConfig{Pprof: true}}, nil },
			args:         []string{"goroutines"},
			wantPath:     "/debug/pprof/goroutine?debug=2",
			wantTimeout:  5 * time.Second,
			wantSockPath: wantSocket,
			wantEnabled:  true,
		},
		{
			name:       "rejects extra goroutines args",
			loadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
			args:       []string{"goroutines", "extra"},
			wantErr:    debugUsage,
		},
		{
			name:         "parses heap",
			loadConfig:   func() (*config.Config, error) { return &config.Config{Debug: config.DebugConfig{Pprof: true}}, nil },
			args:         []string{"heap"},
			wantPath:     "/debug/pprof/heap?debug=1",
			wantTimeout:  5 * time.Second,
			wantSockPath: wantSocket,
			wantEnabled:  true,
		},
		{
			name:       "rejects extra heap args",
			loadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
			args:       []string{"heap", "extra"},
			wantErr:    debugUsage,
		},
		{
			name:         "parses socket",
			loadConfig:   func() (*config.Config, error) { return &config.Config{}, nil },
			args:         []string{"socket"},
			wantPath:     "",
			wantTimeout:  5 * time.Second,
			wantSockPath: wantSocket,
		},
		{
			name:       "rejects extra socket args",
			loadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
			args:       []string{"socket", "extra"},
			wantErr:    debugUsage,
		},
		{
			name:         "parses default profile duration",
			loadConfig:   func() (*config.Config, error) { return &config.Config{Debug: config.DebugConfig{Pprof: true}}, nil },
			args:         []string{"profile"},
			wantPath:     "/debug/pprof/profile?seconds=30",
			wantTimeout:  35 * time.Second,
			wantSockPath: wantSocket,
			wantEnabled:  true,
		},
		{
			name:         "rounds up short profile durations",
			loadConfig:   func() (*config.Config, error) { return &config.Config{Debug: config.DebugConfig{Pprof: true}}, nil },
			args:         []string{"profile", "--duration", "100ms"},
			wantPath:     "/debug/pprof/profile?seconds=1",
			wantTimeout:  5*time.Second + 100*time.Millisecond,
			wantSockPath: wantSocket,
			wantEnabled:  true,
		},
		{
			name:       "rejects invalid profile flag",
			loadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
			args:       []string{"profile", "--seconds", "1s"},
			wantErr:    debugUsage,
		},
		{
			name:       "rejects invalid profile duration",
			loadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
			args:       []string{"profile", "--duration", "later"},
			wantErr:    `invalid profile duration "later"`,
		},
		{
			name:       "rejects unknown subcommand",
			loadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
			args:       []string{"wat"},
			wantErr:    debugUsage,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req, err := parseDebugCommandWithConfigLoader(sessionName, tt.args, tt.loadConfig)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("parseDebugCommand(%q, %v): expected error containing %q", sessionName, tt.args, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseDebugCommand(%q, %v) error = %q, want substring %q", sessionName, tt.args, err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDebugCommand(%q, %v): %v", sessionName, tt.args, err)
			}
			if req.path != tt.wantPath {
				t.Fatalf("path = %q, want %q", req.path, tt.wantPath)
			}
			if req.timeout != tt.wantTimeout {
				t.Fatalf("timeout = %v, want %v", req.timeout, tt.wantTimeout)
			}
			if req.sockPath != tt.wantSockPath {
				t.Fatalf("sockPath = %q, want %q", req.sockPath, tt.wantSockPath)
			}
			if req.configEnabled != tt.wantEnabled {
				t.Fatalf("configEnabled = %v, want %v", req.configEnabled, tt.wantEnabled)
			}
		})
	}
}

func TestRunDebugCommandWithIO(t *testing.T) {
	t.Parallel()

	t.Run("returns parse errors", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		err := runDebugCommandWithConfigLoader(&out, "socket-print-error", nil, func() (*config.Config, error) {
			return &config.Config{}, nil
		})
		if err == nil || !strings.Contains(err.Error(), debugUsage) {
			t.Fatalf("runDebugCommandWithIO() error = %v, want usage", err)
		}
		if out.Len() != 0 {
			t.Fatalf("stdout = %q, want empty", out.String())
		}
	})

	t.Run("prints socket path", func(t *testing.T) {
		t.Parallel()

		sessionName := "socket-print-success"
		sockPath := server.PprofSocketPath(sessionName)
		if err := os.MkdirAll(server.SocketDir(), 0700); err != nil {
			t.Fatalf("MkdirAll(%q): %v", server.SocketDir(), err)
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

		var out bytes.Buffer
		if err := runDebugCommandWithConfigLoader(&out, sessionName, []string{"socket"}, func() (*config.Config, error) {
			return &config.Config{Debug: config.DebugConfig{Pprof: true}}, nil
		}); err != nil {
			t.Fatalf("runDebugCommandWithIO(socket): %v", err)
		}
		if got := out.String(); got != sockPath+"\n" {
			t.Fatalf("stdout = %q, want %q", got, sockPath+"\n")
		}
	})
}

func TestEnsureDebugSocketAvailable(t *testing.T) {
	t.Parallel()

	t.Run("disabled socket reports hint", func(t *testing.T) {
		t.Parallel()

		err := ensureDebugSocketAvailable(debugEndpointRequest{
			sockPath:      filepath.Join(t.TempDir(), "missing.sock"),
			configEnabled: false,
		})
		if err == nil || !strings.Contains(err.Error(), debugDisabledHint) {
			t.Fatalf("ensureDebugSocketAvailable(disabled) error = %v, want disabled hint", err)
		}
	})

	t.Run("enabled socket asks for restart", func(t *testing.T) {
		t.Parallel()

		sockPath := filepath.Join(t.TempDir(), "missing.sock")
		err := ensureDebugSocketAvailable(debugEndpointRequest{
			sockPath:      sockPath,
			configEnabled: true,
		})
		if err == nil || !strings.Contains(err.Error(), "restart the server") {
			t.Fatalf("ensureDebugSocketAvailable(enabled) error = %v, want restart hint", err)
		}
	})

	t.Run("stat errors are surfaced", func(t *testing.T) {
		t.Parallel()

		dir := filepath.Join(t.TempDir(), "restricted")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
		if err := os.Chmod(dir, 0); err != nil {
			t.Fatalf("Chmod(%q, 0): %v", dir, err)
		}
		t.Cleanup(func() {
			_ = os.Chmod(dir, 0700)
		})

		err := ensureDebugSocketAvailable(debugEndpointRequest{
			sockPath:      filepath.Join(dir, "blocked.sock"),
			configEnabled: true,
		})
		if err == nil || !strings.Contains(err.Error(), "stat pprof debug socket:") {
			t.Fatalf("ensureDebugSocketAvailable(stat error) error = %v, want stat failure", err)
		}
	})
}

func TestFetchDebugEndpointErrors(t *testing.T) {
	t.Parallel()

	t.Run("request errors are surfaced", func(t *testing.T) {
		t.Parallel()

		if err := os.MkdirAll(server.SocketDir(), 0700); err != nil {
			t.Fatalf("MkdirAll(%q): %v", server.SocketDir(), err)
		}
		sockPath := filepath.Join(server.SocketDir(), "stale-fetch-"+strings.ReplaceAll(t.Name(), "/", "-")+".sock")
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

		_, err = fetchDebugEndpoint(debugEndpointRequest{
			sockPath:      sockPath,
			path:          "/debug/pprof/goroutine?debug=2",
			timeout:       time.Second,
			configEnabled: true,
		})
		if err == nil || !strings.Contains(err.Error(), "requesting /debug/pprof/goroutine?debug=2:") {
			t.Fatalf("fetchDebugEndpoint(stale socket) error = %v, want request failure", err)
		}
	})

	t.Run("non-ok responses are surfaced", func(t *testing.T) {
		t.Parallel()

		if err := os.MkdirAll(server.SocketDir(), 0700); err != nil {
			t.Fatalf("MkdirAll(%q): %v", server.SocketDir(), err)
		}
		sockPath := filepath.Join(server.SocketDir(), "http-error-"+strings.ReplaceAll(t.Name(), "/", "-")+".sock")
		_ = os.Remove(sockPath)

		ln, err := net.Listen("unix", sockPath)
		if err != nil {
			t.Fatalf("Listen(%q): %v", sockPath, err)
		}
		t.Cleanup(func() {
			_ = ln.Close()
			_ = os.Remove(sockPath)
		})

		srv := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "boom", http.StatusInternalServerError)
			}),
		}
		errCh := make(chan error, 1)
		go func() {
			errCh <- srv.Serve(ln)
		}()
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
			err := <-errCh
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				t.Fatalf("Serve(%q): %v", sockPath, err)
			}
		})

		_, err = fetchDebugEndpoint(debugEndpointRequest{
			sockPath:      sockPath,
			path:          "/debug/pprof/heap?debug=1",
			timeout:       time.Second,
			configEnabled: true,
		})
		if err == nil || !strings.Contains(err.Error(), "/debug/pprof/heap?debug=1 returned 500 Internal Server Error:") {
			t.Fatalf("fetchDebugEndpoint(http error) error = %v, want HTTP status failure", err)
		}
	})
}
