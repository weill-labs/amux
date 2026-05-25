package cli

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
)

func TestRunDiagCommandFetchesExpectedEndpoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		wantPath    string
		wantOutput  string
		wantFile    string
		wantTimeout time.Duration
	}{
		{
			name:        "default dumps goroutines",
			wantPath:    "/debug/pprof/goroutine?debug=2",
			wantOutput:  "profile-body",
			wantTimeout: 5 * time.Second,
		},
		{
			name:        "dump dumps goroutines",
			args:        []string{"dump"},
			wantPath:    "/debug/pprof/goroutine?debug=2",
			wantOutput:  "profile-body",
			wantTimeout: 5 * time.Second,
		},
		{
			name:        "heap writes binary profile to stdout",
			args:        []string{"heap"},
			wantPath:    "/debug/pprof/heap?gc=1",
			wantOutput:  "profile-body",
			wantTimeout: 5 * time.Second,
		},
		{
			name:        "heap writes binary profile to output file",
			args:        []string{"heap", "--output", "heap.pprof"},
			wantPath:    "/debug/pprof/heap?gc=1",
			wantFile:    "heap.pprof",
			wantTimeout: 5 * time.Second,
		},
		{
			name:        "profile defaults to ten seconds",
			args:        []string{"profile"},
			wantPath:    "/debug/pprof/profile?seconds=10",
			wantOutput:  "profile-body",
			wantTimeout: 15 * time.Second,
		},
		{
			name:        "profile accepts seconds and output file",
			args:        []string{"profile", "--seconds", "2", "--output", "cpu.pprof"},
			wantPath:    "/debug/pprof/profile?seconds=2",
			wantFile:    "cpu.pprof",
			wantTimeout: 7 * time.Second,
		},
		{
			name:        "generic pprof passes name through",
			args:        []string{"pprof", "block"},
			wantPath:    "/debug/pprof/block",
			wantOutput:  "profile-body",
			wantTimeout: 5 * time.Second,
		},
		{
			name:        "generic trace passes name through",
			args:        []string{"pprof", "trace", "--output", "trace.out"},
			wantPath:    "/debug/pprof/trace",
			wantFile:    "trace.out",
			wantTimeout: 5 * time.Second,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			var wrotePath string
			var wroteData []byte
			err := runDiagCommandWithDeps(context.Background(), &out, "diag-session", tt.args, diagDeps{
				loadConfig: func() (*config.Config, error) {
					return &config.Config{Debug: config.DebugConfig{Pprof: true}}, nil
				},
				discoverSocket: func(ctx context.Context, session string, cfg *config.Config) (string, error) {
					if session != "diag-session" {
						t.Fatalf("session = %q, want diag-session", session)
					}
					return "/tmp/live.pprof", nil
				},
				fetch: func(req debugEndpointRequest) ([]byte, error) {
					if req.sockPath != "/tmp/live.pprof" {
						t.Fatalf("sockPath = %q, want /tmp/live.pprof", req.sockPath)
					}
					if req.path != tt.wantPath {
						t.Fatalf("path = %q, want %q", req.path, tt.wantPath)
					}
					if req.timeout != tt.wantTimeout {
						t.Fatalf("timeout = %v, want %v", req.timeout, tt.wantTimeout)
					}
					return []byte("profile-body"), nil
				},
				writeFile: func(path string, data []byte, perm fs.FileMode) error {
					wrotePath = path
					wroteData = append([]byte(nil), data...)
					if perm != 0600 {
						t.Fatalf("perm = %#o, want 0600", perm)
					}
					return nil
				},
			})
			if err != nil {
				t.Fatalf("runDiagCommandWithDeps(%v): %v", tt.args, err)
			}
			if got := out.String(); got != tt.wantOutput {
				t.Fatalf("stdout = %q, want %q", got, tt.wantOutput)
			}
			if wrotePath != tt.wantFile {
				t.Fatalf("wrote path = %q, want %q", wrotePath, tt.wantFile)
			}
			if tt.wantFile != "" && string(wroteData) != "profile-body" {
				t.Fatalf("wrote data = %q, want profile-body", wroteData)
			}
		})
	}
}

func TestRunDiagGoroutinesSummarizesStateHistogram(t *testing.T) {
	t.Parallel()

	dump := `goroutine 1 [running]:
main.main()

goroutine 7 [IO wait, 2 minutes]:
net.(*pollDesc).wait()

goroutine 8 [chan receive]:
main.worker()
`

	var out bytes.Buffer
	err := runDiagCommandWithDeps(context.Background(), &out, "diag-session", []string{"goroutines"}, diagDeps{
		loadConfig: func() (*config.Config, error) {
			return &config.Config{Debug: config.DebugConfig{Pprof: true}}, nil
		},
		discoverSocket: func(context.Context, string, *config.Config) (string, error) {
			return "/tmp/live.pprof", nil
		},
		fetch: func(req debugEndpointRequest) ([]byte, error) {
			if req.path != "/debug/pprof/goroutine?debug=2" {
				t.Fatalf("path = %q, want goroutine dump path", req.path)
			}
			return []byte(dump), nil
		},
	})
	if err != nil {
		t.Fatalf("runDiagCommandWithDeps(goroutines): %v", err)
	}

	want := "goroutines: 3\nstates:\n  IO wait: 1\n  chan receive: 1\n  running: 1\n"
	if got := out.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunDiagInfoFormatsEndpointAndWatchdogLines(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "diag-session.log")
	logData := strings.Join([]string{
		`{"event":"startup"}`,
		`{"event":"event_loop_watchdog","elapsed":"31s","command_type":"server.sessionEventCommand"}`,
		`plain event_loop_watchdog fallback line`,
		`{"event":"shutdown"}`,
	}, "\n")

	var out bytes.Buffer
	err := runDiagCommandWithDeps(context.Background(), &out, "diag-session", []string{"info"}, diagDeps{
		loadConfig: func() (*config.Config, error) {
			return &config.Config{Debug: config.DebugConfig{Pprof: true}}, nil
		},
		discoverSocket: func(context.Context, string, *config.Config) (string, error) {
			return "/tmp/live.pprof", nil
		},
		fetch: func(req debugEndpointRequest) ([]byte, error) {
			if req.path != "/debug/amux/info" {
				t.Fatalf("path = %q, want /debug/amux/info", req.path)
			}
			return []byte(`{"pid":123,"uptime":"2s","binary":"/bin/amux","build":"abc123","go_version":"go1.25.0","goroutines":17}`), nil
		},
		readFile: func(path string) ([]byte, error) {
			if path != logPath {
				t.Fatalf("log path = %q, want %q", path, logPath)
			}
			return []byte(logData), nil
		},
		logPath: func(session string) string {
			if session != "diag-session" {
				t.Fatalf("session = %q, want diag-session", session)
			}
			return logPath
		},
	})
	if err != nil {
		t.Fatalf("runDiagCommandWithDeps(info): %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"pid: 123\n",
		"uptime: 2s\n",
		"binary: /bin/amux\n",
		"build: abc123\n",
		"go: go1.25.0\n",
		"goroutines: 17\n",
		"recent event_loop_watchdog log lines:\n",
		`{"event":"event_loop_watchdog","elapsed":"31s","command_type":"server.sessionEventCommand"}`,
		"plain event_loop_watchdog fallback line",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("info output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"event":"startup"`) {
		t.Fatalf("info output included unrelated log line:\n%s", got)
	}
}

func TestRunDiagMissingPprofErrorNamesConfigAndRestart(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := runDiagCommandWithDeps(context.Background(), &out, "diag-session", []string{"dump"}, diagDeps{
		loadConfig: func() (*config.Config, error) {
			return &config.Config{}, nil
		},
		discoverSocket: func(context.Context, string, *config.Config) (string, error) {
			return "", os.ErrNotExist
		},
	})
	if err == nil {
		t.Fatal("runDiagCommandWithDeps(dump) error = nil, want missing pprof error")
	}
	for _, want := range []string{"[debug] pprof = true", "~/.config/amux/config.toml", "restart"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want substring %q", err.Error(), want)
		}
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
}

func TestRunDiagCommandRejectsInvalidArgs(t *testing.T) {
	t.Parallel()

	tests := [][]string{
		{"heap", "--output"},
		{"profile", "--seconds", "0"},
		{"profile", "--duration", "1s"},
		{"pprof"},
		{"pprof", "../heap"},
		{"info", "--output", "info.txt"},
		{"unknown"},
	}

	for _, args := range tests {
		args := args
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			err := runDiagCommandWithDeps(context.Background(), &out, "diag-session", args, diagDeps{
				loadConfig: func() (*config.Config, error) {
					t.Fatal("loadConfig should not be called for invalid args")
					return nil, nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), diagUsage) {
				t.Fatalf("runDiagCommandWithDeps(%v) error = %v, want usage", args, err)
			}
		})
	}
}

func TestDiscoverDiagPprofSocketUsesLivePIDFromSS(t *testing.T) {
	t.Parallel()

	session := "diag-session"
	mainSocket := "/tmp/amux-1000/diag-session"
	pprofSocket := "/tmp/amux-1000/diag-session.pprof"
	staleSocket := "/tmp/amux-1000/stale.pprof"
	ssOutput := strings.Join([]string{
		`u_str LISTEN 0 4096 ` + mainSocket + ` 123 * 0 users:(("amux",pid=4242,fd=7))`,
		`u_str LISTEN 0 4096 ` + staleSocket + ` 123 * 0 users:(("amux",pid=9999,fd=7))`,
		`u_str LISTEN 0 4096 ` + pprofSocket + ` 123 * 0 users:(("amux",pid=4242,fd=8))`,
	}, "\n")

	var probed []string
	got, err := discoverDiagPprofSocketWithDeps(context.Background(), session, &config.Config{Debug: config.DebugConfig{Pprof: true}}, diagDiscoveryDeps{
		serverSocketPath: func(string) string { return mainSocket },
		pprofSocketPath:  func(string) string { return "/tmp/amux-1000/diag-session.pprof" },
		runSS: func(context.Context) ([]byte, error) {
			return []byte(ssOutput), nil
		},
		glob: func(string) ([]string, error) {
			t.Fatal("glob fallback should not be used when ss resolves the pprof socket")
			return nil, nil
		},
		probe: func(ctx context.Context, path string) error {
			probed = append(probed, path)
			if path != pprofSocket {
				t.Fatalf("probed path = %q, want %q", path, pprofSocket)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("discoverDiagPprofSocketWithDeps: %v", err)
	}
	if got != pprofSocket {
		t.Fatalf("socket = %q, want %q", got, pprofSocket)
	}
	if len(probed) != 1 {
		t.Fatalf("probed = %v, want one probe", probed)
	}
}

func TestDiscoverDiagPprofSocketFallsBackToProbedSockets(t *testing.T) {
	t.Parallel()

	session := "diag-session"
	liveSocket := "/tmp/amux-1000/diag-session.pprof"
	staleSocket := "/tmp/amux-1000/other.pprof"

	var probes []string
	got, err := discoverDiagPprofSocketWithDeps(context.Background(), session, &config.Config{Debug: config.DebugConfig{Pprof: true}}, diagDiscoveryDeps{
		serverSocketPath: func(string) string { return "/tmp/amux-1000/diag-session" },
		pprofSocketPath:  func(string) string { return liveSocket },
		runSS: func(context.Context) ([]byte, error) {
			return nil, errors.New("ss unavailable")
		},
		glob: func(pattern string) ([]string, error) {
			if !strings.HasSuffix(pattern, "*.pprof") {
				t.Fatalf("glob pattern = %q, want *.pprof", pattern)
			}
			return []string{staleSocket, liveSocket}, nil
		},
		probe: func(ctx context.Context, path string) error {
			probes = append(probes, path)
			if path == liveSocket {
				return nil
			}
			return errors.New("stale")
		},
	})
	if err != nil {
		t.Fatalf("discoverDiagPprofSocketWithDeps fallback: %v", err)
	}
	if got != liveSocket {
		t.Fatalf("socket = %q, want %q", got, liveSocket)
	}
	if len(probes) != 1 || probes[0] != liveSocket {
		t.Fatalf("probes = %v, want only exact session socket probe", probes)
	}
}
