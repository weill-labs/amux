package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/server"
)

func TestParseDoctorOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    doctorOptions
		wantErr bool
	}{
		{name: "empty", want: doctorOptions{}},
		{name: "json quiet verbose all sessions check", args: []string{"--json", "-q", "-v", "--all-sessions", "server-reachable"}, want: doctorOptions{json: true, quiet: true, verbose: true, allSessions: true, checkName: "server-reachable"}},
		{name: "long quiet", args: []string{"--quiet", "pprof"}, want: doctorOptions{quiet: true, checkName: "pprof"}},
		{name: "unknown flag", args: []string{"--bogus"}, wantErr: true},
		{name: "unknown check", args: []string{"bogus"}, wantErr: true},
		{name: "two checks", args: []string{"pprof", "config"}, wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseDoctorOptions(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseDoctorOptions(%v) error = nil, want error", tt.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDoctorOptions(%v): %v", tt.args, err)
			}
			if got != tt.want {
				t.Fatalf("parseDoctorOptions(%v) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}

func TestRunDoctorWithInjectedHealthyDeps(t *testing.T) {
	t.Parallel()

	deps := newHealthyDoctorDeps(t, "unit")
	report := runDoctor(context.Background(), "unit", doctorOptions{}, deps)

	if report.Overall != doctorStatusOK || report.ExitCode != 0 {
		t.Fatalf("report = %+v, want ok exit 0", report)
	}
	requireDoctorCheckStatus(t, report.Checks, "config", doctorStatusOK)
	requireDoctorCheckStatus(t, report.Checks, "server-reachable", doctorStatusOK)
	requireDoctorCheckStatus(t, report.Checks, "socket", doctorStatusOK)
	requireDoctorCheckStatus(t, report.Checks, "goroutines", doctorStatusOK)
}

func TestRunDoctorAllSessionsDiscoversSocketEntries(t *testing.T) {
	t.Parallel()

	deps := newHealthyDoctorDeps(t, "alpha")
	betaSocket := filepath.Join(deps.socketDir(), "beta")
	ln := listenDoctorSockets(t, betaSocket)[0]
	t.Cleanup(func() { _ = ln.Close() })

	report := runDoctor(context.Background(), "alpha", doctorOptions{allSessions: true, checkName: "server-reachable"}, deps)
	if report.Overall != doctorStatusOK {
		t.Fatalf("overall = %s, want ok\n%+v", report.Overall, report.Checks)
	}
	if len(report.Checks) != 2 {
		t.Fatalf("checks = %d, want one per discovered session: %+v", len(report.Checks), report.Checks)
	}
	if report.Checks[0].Session != "alpha" || report.Checks[1].Session != "beta" {
		t.Fatalf("sessions = %q, %q; want alpha, beta", report.Checks[0].Session, report.Checks[1].Session)
	}
}

func TestWriteDoctorReportQuietAndVerbose(t *testing.T) {
	t.Parallel()

	report := doctorReport{
		Overall: doctorStatusWarn,
		Session: "unit",
		Checks: []doctorCheckResult{
			{Name: "ok-check", Status: doctorStatusOK, Summary: "fine"},
			{Name: "warn-check", Status: doctorStatusWarn, Summary: "soft", Hint: "fix it", Detail: "line 1\nline 2"},
		},
	}
	var out strings.Builder
	writeDoctorReport(&out, report, doctorOptions{quiet: true, verbose: true})
	got := out.String()
	if strings.Contains(got, "ok-check") {
		t.Fatalf("quiet output included ok check:\n%s", got)
	}
	for _, want := range []string{"overall: warn", "[warn] warn-check: soft", "hint: fix it", "line 1", "line 2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestRunDoctorNamedChecksReportExpectedSeverities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		checkName string
		deps      func(*testing.T) doctorDeps
		want      doctorStatus
		wantHint  string
	}{
		{
			name:      "pprof disabled warns",
			checkName: "pprof",
			deps: func(t *testing.T) doctorDeps {
				deps := newHealthyDoctorDeps(t, "unit")
				deps.loadConfig = func(string) (*config.Config, error) { return &config.Config{}, nil }
				return deps
			},
			want:     doctorStatusWarn,
			wantHint: "Set `[debug] pprof = true`",
		},
		{
			name:      "config parse failure fails",
			checkName: "config",
			deps: func(t *testing.T) doctorDeps {
				deps := newHealthyDoctorDeps(t, "unit")
				deps.loadConfig = func(string) (*config.Config, error) { return nil, errors.New("parsing config: line 1") }
				return deps
			},
			want:     doctorStatusFail,
			wantHint: "TOML parse error",
		},
		{
			name:      "server unreachable fails",
			checkName: "server-reachable",
			deps: func(t *testing.T) doctorDeps {
				deps := newHealthyDoctorDeps(t, "unit")
				deps.runServerCommand = func(context.Context, string, string, []string) (string, error) {
					return "", errors.New("i/o timeout")
				}
				return deps
			},
			want:     doctorStatusFail,
			wantHint: "Server unresponsive",
		},
		{
			name:      "stale runtime warns",
			checkName: "stale-runtime",
			deps: func(t *testing.T) doctorDeps {
				deps := newHealthyDoctorDeps(t, "unit")
				lockPath := filepath.Join(deps.socketDir(), "old.lock")
				if err := os.WriteFile(lockPath, []byte("lock"), 0600); err != nil {
					t.Fatalf("WriteFile(%q): %v", lockPath, err)
				}
				old := deps.now().Add(-8 * 24 * time.Hour)
				if err := os.Chtimes(lockPath, old, old); err != nil {
					t.Fatalf("Chtimes(%q): %v", lockPath, err)
				}
				return deps
			},
			want:     doctorStatusWarn,
			wantHint: "old.lock",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			report := runDoctor(context.Background(), "unit", doctorOptions{checkName: tt.checkName}, tt.deps(t))
			if report.Overall != tt.want {
				t.Fatalf("overall = %s, want %s\n%+v", report.Overall, tt.want, report.Checks)
			}
			if len(report.Checks) != 1 {
				t.Fatalf("checks = %d, want 1", len(report.Checks))
			}
			check := report.Checks[0]
			if check.Status != tt.want {
				t.Fatalf("check status = %s, want %s", check.Status, tt.want)
			}
			if tt.wantHint != "" && !strings.Contains(check.Hint, tt.wantHint) && !strings.Contains(check.Summary, tt.wantHint) {
				t.Fatalf("check = %+v, want hint/summary containing %q", check, tt.wantHint)
			}
		})
	}
}

func TestDoctorSocketCheckFailsOnOwnerMismatch(t *testing.T) {
	t.Parallel()

	deps := newHealthyDoctorDeps(t, "unit")
	deps.runSS = func(context.Context) ([]byte, error) {
		return []byte("u_str LISTEN 0 4096 " + deps.socketPath("unit") + ` 123 * 0 users:(("amux",pid=999,fd=7))`), nil
	}

	report := runDoctor(context.Background(), "unit", doctorOptions{checkName: "socket"}, deps)
	if report.Overall != doctorStatusFail {
		t.Fatalf("overall = %s, want fail\n%+v", report.Overall, report.Checks)
	}
	if !strings.Contains(report.Checks[0].Hint, "expected pid 123") {
		t.Fatalf("socket hint = %q, want owner mismatch", report.Checks[0].Hint)
	}
}

func TestDoctorLogParsers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.May, 27, 12, 0, 0, 0, time.UTC)
	clientLog := "TS                             EVENT    CLIENT     COLS   ROWS   REASON\n"
	for i := 0; i < 6; i++ {
		clientLog += now.Add(-time.Duration(i)*time.Second).Format(time.RFC3339Nano) + " attach   client-1   80     24     -\n"
	}
	if got := clientReconnectCounts(clientLog, now)["client-1"]; got != 6 {
		t.Fatalf("client reconnect count = %d, want 6", got)
	}

	paneLog := "TS                             EVENT    ID    PANE         HOST       CWD                                      GIT_BRANCH               REASON\n"
	paneLog += now.Add(-time.Minute).Format(time.RFC3339Nano) + " exit     2     pane-2       local      /tmp                                     main                     exit 1\n"
	paneLog += now.Add(-time.Minute).Format(time.RFC3339Nano) + " exit     3     pane-3       local      /tmp                                     main                     exit 0\n"
	if got := abnormalPaneExitCount(paneLog, now); got != 1 {
		t.Fatalf("abnormalPaneExitCount = %d, want 1", got)
	}

	lines := []string{
		`{"event":"event_loop_watchdog","time":"2026/05/27 11:59:00"}`,
		`{"event":"event_loop_watchdog","time":"2026/05/25 11:59:00"}`,
		`plain event_loop_watchdog line`,
	}
	if got := recentWatchdogCount(lines, now); got != 2 {
		t.Fatalf("recentWatchdogCount = %d, want 2", got)
	}
}

func newHealthyDoctorDeps(t *testing.T, session string) doctorDeps {
	t.Helper()

	dir := t.TempDir()
	socketDir := filepath.Join(dir, "sockets")
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", socketDir, err)
	}
	mainSocket := filepath.Join(socketDir, session)
	pprofSocket := filepath.Join(socketDir, session+".pprof")
	listeners := listenDoctorSockets(t, mainSocket, pprofSocket)
	t.Cleanup(func() {
		for _, ln := range listeners {
			_ = ln.Close()
		}
	})

	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte("[debug]\npprof = true\n"), 0600); err != nil {
		t.Fatalf("WriteFile(%q): %v", configPath, err)
	}
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", home, err)
	}
	now := time.Date(2026, time.May, 27, 12, 0, 0, 0, time.UTC)
	infoJSON, err := json.Marshal(server.DiagInfo{
		PID:        123,
		Uptime:     "2s",
		Binary:     os.Args[0],
		Build:      "dev",
		GoVersion:  "go1.25.0",
		Goroutines: 17,
	})
	if err != nil {
		t.Fatalf("Marshal diag info: %v", err)
	}

	return doctorDeps{
		now:             func() time.Time { return now },
		configPath:      func() string { return configPath },
		hostsPath:       func() string { return filepath.Join(dir, "hosts.toml") },
		loadConfig:      config.Load,
		readFile:        os.ReadFile,
		readDir:         os.ReadDir,
		lstat:           os.Lstat,
		stat:            os.Stat,
		userHomeDir:     func() (string, error) { return home, nil },
		socketDir:       func() string { return socketDir },
		socketPath:      func(string) string { return mainSocket },
		pprofSocketPath: func(string) string { return pprofSocket },
		runServerCommand: func(_ context.Context, _ string, cmd string, _ []string) (string, error) {
			switch cmd {
			case "list":
				return "pane-1 local\n", nil
			case "connection-log":
				return "No client connections recorded.\n", nil
			case "pane-log":
				return "No pane lifecycle events recorded.\n", nil
			default:
				return "", errors.New("unexpected command " + cmd)
			}
		},
		runSS: func(context.Context) ([]byte, error) {
			return []byte(
				"u_str LISTEN 0 4096 " + mainSocket + ` 123 * 0 users:(("amux",pid=123,fd=7))` + "\n" +
					"u_str LISTEN 0 4096 " + pprofSocket + ` 123 * 0 users:(("amux",pid=123,fd=8))`,
			), nil
		},
		listProcesses: func(context.Context) ([]doctorProcess, error) {
			return []doctorProcess{{pid: 123, command: "amux _server " + session}}, nil
		},
		diskUsage: func(string) (doctorDiskUsage, error) {
			return doctorDiskUsage{percent: 10}, nil
		},
		discoverPprof: func(context.Context, string, *config.Config) (string, error) {
			return pprofSocket, nil
		},
		fetchEndpoint: func(req debugEndpointRequest) ([]byte, error) {
			switch req.path {
			case "/debug/amux/info":
				return infoJSON, nil
			case "/debug/pprof/goroutine?debug=2":
				return []byte("goroutine 1 [running]:\nmain.main()\n\ngoroutine 2 [IO wait]:\nnet.(*pollDesc).wait()\n"), nil
			default:
				return nil, errors.New("unexpected endpoint " + req.path)
			}
		},
	}
}

func listenDoctorSockets(t *testing.T, paths ...string) []net.Listener {
	t.Helper()

	listeners := make([]net.Listener, 0, len(paths))
	for _, path := range paths {
		ln, err := net.Listen("unix", path)
		if err != nil {
			for _, existing := range listeners {
				_ = existing.Close()
			}
			t.Fatalf("Listen(%q): %v", path, err)
		}
		listeners = append(listeners, ln)
	}
	return listeners
}

func requireDoctorCheckStatus(t *testing.T, checks []doctorCheckResult, name string, status doctorStatus) {
	t.Helper()

	for _, check := range checks {
		if check.Name == name {
			if check.Status != status {
				t.Fatalf("check %q status = %s, want %s", name, check.Status, status)
			}
			return
		}
	}
	t.Fatalf("missing check %q in %+v", name, checks)
}
