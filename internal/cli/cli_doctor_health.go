package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/dialutil"
	"github.com/weill-labs/amux/internal/server"
)

const (
	doctorSchemaVersion        = 1
	doctorDefaultTimeout       = 5 * time.Second
	doctorStaleRuntimeAge      = 7 * 24 * time.Hour
	doctorGoroutineWarn        = 500
	doctorGoroutineBucketWarn  = 250
	doctorClientFlapWarn       = 5
	doctorRecentClientWindow   = time.Minute
	doctorRecentPaneExitWindow = 5 * time.Minute
	doctorWatchdogWindow       = 24 * time.Hour
	doctorDiskWarnPercent      = 90
)

type doctorStatus string

const (
	doctorStatusOK   doctorStatus = "ok"
	doctorStatusWarn doctorStatus = "warn"
	doctorStatusFail doctorStatus = "fail"
)

type doctorOptions struct {
	json        bool
	allSessions bool
	quiet       bool
	verbose     bool
	checkName   string
}

type doctorReport struct {
	SchemaVersion int                 `json:"schema_version"`
	Overall       doctorStatus        `json:"overall"`
	ExitCode      int                 `json:"exit_code"`
	Session       string              `json:"session"`
	GeneratedAt   string              `json:"generated_at"`
	Checks        []doctorCheckResult `json:"checks"`
}

type doctorCheckResult struct {
	Name    string       `json:"name"`
	Scope   string       `json:"scope"`
	Session string       `json:"session,omitempty"`
	Status  doctorStatus `json:"status"`
	Summary string       `json:"summary"`
	Hint    string       `json:"hint,omitempty"`
	Detail  string       `json:"-"`
}

type doctorDeps struct {
	now              func() time.Time
	configPath       func() string
	hostsPath        func() string
	loadConfig       func(string) (*config.Config, error)
	readFile         func(string) ([]byte, error)
	readDir          func(string) ([]os.DirEntry, error)
	lstat            func(string) (os.FileInfo, error)
	stat             func(string) (os.FileInfo, error)
	userHomeDir      func() (string, error)
	socketDir        func() string
	socketPath       func(string) string
	pprofSocketPath  func(string) string
	runServerCommand func(context.Context, string, string, []string) (string, error)
	runSS            func(context.Context) ([]byte, error)
	listProcesses    func(context.Context) ([]doctorProcess, error)
	diskUsage        func(string) (doctorDiskUsage, error)
	discoverPprof    func(context.Context, string, *config.Config) (string, error)
	fetchEndpoint    func(debugEndpointRequest) ([]byte, error)
}

type doctorProcess struct {
	pid     int
	command string
}

type doctorDiskUsage struct {
	percent int
}

type doctorRunState struct {
	deps         doctorDeps
	opts         doctorOptions
	configLoaded bool
	configPath   string
	config       *config.Config
	configErr    error
	diagInfo     map[string]doctorDiagInfoResult
}

type doctorDiagInfoResult struct {
	info server.DiagInfo
	err  error
}

type doctorCheckDef struct {
	name  string
	scope string
	run   func(context.Context, *doctorRunState, string) doctorCheckResult
}

func runDoctorCommand(inv invocation, args []string) int {
	if len(args) == 1 && args[0] == "fonts" {
		writeFontDiagnostics(inv.runtime.Stdout)
		return 0
	}

	opts, err := parseDoctorOptions(args)
	if err != nil {
		fmt.Fprintln(inv.runtime.Stderr, doctorUsage)
		return 1
	}

	report := runDoctor(context.Background(), inv.sessionName, opts, doctorDeps{})
	if opts.json {
		enc := json.NewEncoder(inv.runtime.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(inv.runtime.Stderr, "amux doctor: %v\n", err)
			return 2
		}
	} else {
		writeDoctorReport(inv.runtime.Stdout, report, opts)
	}
	return report.ExitCode
}

func parseDoctorOptions(args []string) (doctorOptions, error) {
	var opts doctorOptions
	for _, arg := range args {
		switch arg {
		case "--json":
			opts.json = true
		case "--all-sessions":
			opts.allSessions = true
		case "--quiet", "-q":
			opts.quiet = true
		case "--verbose", "-v":
			opts.verbose = true
		default:
			if strings.HasPrefix(arg, "-") || opts.checkName != "" {
				return doctorOptions{}, errors.New(doctorUsage)
			}
			opts.checkName = arg
		}
	}
	if opts.checkName != "" && doctorCheckByName(opts.checkName) == nil {
		return doctorOptions{}, errors.New(doctorUsage)
	}
	return opts, nil
}

func runDoctor(ctx context.Context, sessionName string, opts doctorOptions, deps doctorDeps) doctorReport {
	deps = fillDoctorDeps(deps)
	state := &doctorRunState{
		deps:     deps,
		opts:     opts,
		diagInfo: make(map[string]doctorDiagInfoResult),
	}

	sessions := []string{sessionName}
	if opts.allSessions {
		if discovered, err := discoverDoctorSessions(deps); err == nil && len(discovered) > 0 {
			sessions = discovered
		}
	}

	checks := runDoctorChecks(ctx, state, opts, sessions)
	overall := doctorStatusOK
	for _, check := range checks {
		overall = maxDoctorStatus(overall, check.Status)
	}
	return doctorReport{
		SchemaVersion: doctorSchemaVersion,
		Overall:       overall,
		ExitCode:      doctorExitCode(overall),
		Session:       sessionName,
		GeneratedAt:   deps.now().UTC().Format(time.RFC3339Nano),
		Checks:        checks,
	}
}

func fillDoctorDeps(deps doctorDeps) doctorDeps {
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.configPath == nil {
		deps.configPath = config.DefaultPath
	}
	if deps.hostsPath == nil {
		deps.hostsPath = func() string {
			return filepath.Join(filepath.Dir(deps.configPath()), "hosts.toml")
		}
	}
	if deps.loadConfig == nil {
		deps.loadConfig = config.Load
	}
	if deps.readFile == nil {
		deps.readFile = os.ReadFile
	}
	if deps.readDir == nil {
		deps.readDir = os.ReadDir
	}
	if deps.lstat == nil {
		deps.lstat = os.Lstat
	}
	if deps.stat == nil {
		deps.stat = os.Stat
	}
	if deps.userHomeDir == nil {
		deps.userHomeDir = os.UserHomeDir
	}
	if deps.socketDir == nil {
		deps.socketDir = server.SocketDir
	}
	if deps.socketPath == nil {
		deps.socketPath = server.SocketPath
	}
	if deps.pprofSocketPath == nil {
		deps.pprofSocketPath = server.PprofSocketPath
	}
	if deps.runServerCommand == nil {
		deps.runServerCommand = runDoctorServerCommand
	}
	if deps.runSS == nil {
		deps.runSS = runSSUnixListeners
	}
	if deps.listProcesses == nil {
		deps.listProcesses = listDoctorProcesses
	}
	if deps.diskUsage == nil {
		deps.diskUsage = doctorDiskUsageForPath
	}
	if deps.discoverPprof == nil {
		deps.discoverPprof = discoverDiagPprofSocket
	}
	if deps.fetchEndpoint == nil {
		deps.fetchEndpoint = fetchDebugEndpoint
	}
	return deps
}

func doctorCheckDefs() []doctorCheckDef {
	return []doctorCheckDef{
		{name: "config", scope: "global", run: doctorCheckConfig},
		{name: "hosts", scope: "global", run: doctorCheckHosts},
		{name: "pprof", scope: "global", run: doctorCheckPprof},
		{name: "fonts", scope: "global", run: doctorCheckFonts},
		{name: "stale-runtime", scope: "global", run: doctorCheckStaleRuntime},
		{name: "disk-pressure", scope: "global", run: doctorCheckDiskPressure},
		{name: "server-reachable", scope: "session", run: doctorCheckServerReachable},
		{name: "server-processes", scope: "session", run: doctorCheckServerProcesses},
		{name: "socket", scope: "session", run: doctorCheckSocket},
		{name: "diag-info", scope: "session", run: doctorCheckDiagInfo},
		{name: "goroutines", scope: "session", run: doctorCheckGoroutines},
		{name: "clients-log", scope: "session", run: doctorCheckClientsLog},
		{name: "panes-log", scope: "session", run: doctorCheckPanesLog},
		{name: "watchdog", scope: "session", run: doctorCheckWatchdog},
		{name: "binary", scope: "session", run: doctorCheckBinary},
	}
}

func doctorCheckByName(name string) *doctorCheckDef {
	for _, def := range doctorCheckDefs() {
		if def.name == name {
			def := def
			return &def
		}
	}
	return nil
}

func runDoctorChecks(ctx context.Context, state *doctorRunState, opts doctorOptions, sessions []string) []doctorCheckResult {
	var checks []doctorCheckResult
	for _, def := range doctorCheckDefs() {
		if opts.checkName != "" && opts.checkName != def.name {
			continue
		}
		switch def.scope {
		case "global":
			checks = append(checks, def.run(ctx, state, ""))
		case "session":
			for _, session := range sessions {
				checks = append(checks, def.run(ctx, state, session))
			}
		}
	}
	return checks
}

func checkOK(name, scope, session, summary string) doctorCheckResult {
	return doctorCheckResult{Name: name, Scope: scope, Session: session, Status: doctorStatusOK, Summary: summary}
}

func checkWarn(name, scope, session, summary, hint string) doctorCheckResult {
	return doctorCheckResult{Name: name, Scope: scope, Session: session, Status: doctorStatusWarn, Summary: summary, Hint: hint}
}

func checkFail(name, scope, session, summary, hint string) doctorCheckResult {
	return doctorCheckResult{Name: name, Scope: scope, Session: session, Status: doctorStatusFail, Summary: summary, Hint: hint}
}

func (s *doctorRunState) loadConfig() (*config.Config, error) {
	if !s.configLoaded {
		s.configLoaded = true
		s.configPath = s.deps.configPath()
		s.config, s.configErr = s.deps.loadConfig(s.configPath)
	}
	return s.config, s.configErr
}

func (s *doctorRunState) diagInfoForSession(ctx context.Context, session string) doctorDiagInfoResult {
	if got, ok := s.diagInfo[session]; ok {
		return got
	}
	ctx, cancel := context.WithTimeout(ctx, doctorDefaultTimeout)
	defer cancel()

	cfg, err := s.loadConfig()
	if err != nil {
		res := doctorDiagInfoResult{err: fmt.Errorf("loading config: %w", err)}
		s.diagInfo[session] = res
		return res
	}
	sockPath, err := s.deps.discoverPprof(ctx, session, cfg)
	if err != nil {
		res := doctorDiagInfoResult{err: err}
		s.diagInfo[session] = res
		return res
	}
	body, err := s.deps.fetchEndpoint(debugEndpointRequest{
		sockPath:      sockPath,
		path:          "/debug/amux/info",
		timeout:       doctorDefaultTimeout,
		configEnabled: true,
		missingHint:   doctorDiagMissingSocketHint(session),
	})
	if err != nil {
		res := doctorDiagInfoResult{err: err}
		s.diagInfo[session] = res
		return res
	}
	var info server.DiagInfo
	if err := json.Unmarshal(body, &info); err != nil {
		res := doctorDiagInfoResult{err: fmt.Errorf("decoding _diag info: %w", err)}
		s.diagInfo[session] = res
		return res
	}
	res := doctorDiagInfoResult{info: info}
	s.diagInfo[session] = res
	return res
}

func doctorCheckConfig(_ context.Context, state *doctorRunState, _ string) doctorCheckResult {
	path := state.deps.configPath()
	_, err := state.loadConfig()
	if err != nil {
		return checkFail("config", "global", "", "config.toml failed to parse", "TOML parse error at "+path+": "+err.Error())
	}
	if _, err := state.deps.stat(path); errors.Is(err, os.ErrNotExist) {
		return checkOK("config", "global", "", path+" not present; built-in defaults apply")
	}
	return checkOK("config", "global", "", path+" parses cleanly")
}

func doctorCheckHosts(_ context.Context, state *doctorRunState, _ string) doctorCheckResult {
	path := state.deps.hostsPath()
	data, err := state.deps.readFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return checkOK("hosts", "global", "", path+" not present")
		}
		return checkFail("hosts", "global", "", "hosts.toml could not be read", err.Error())
	}
	var decoded map[string]any
	if _, err := toml.Decode(string(data), &decoded); err != nil {
		return checkFail("hosts", "global", "", "hosts.toml failed to parse", "TOML parse error at "+path+": "+err.Error())
	}
	return checkOK("hosts", "global", "", path+" parses cleanly")
}

func doctorCheckPprof(_ context.Context, state *doctorRunState, _ string) doctorCheckResult {
	cfg, err := state.loadConfig()
	if err != nil {
		return checkFail("pprof", "global", "", "cannot determine pprof setting because config failed to parse", err.Error())
	}
	if cfg.PprofEnabled() {
		return checkOK("pprof", "global", "", "[debug] pprof = true")
	}
	return checkWarn(
		"pprof",
		"global",
		"",
		"[debug] pprof is disabled",
		"Set `[debug] pprof = true` and restart server so `_diag` and `amux debug` can reach the pprof endpoint.",
	)
}

func doctorCheckFonts(_ context.Context, state *doctorRunState, _ string) doctorCheckResult {
	res := checkOK("fonts", "global", "", "font samples are available; run `amux doctor fonts` if glyphs look wrong")
	if state.opts.verbose {
		var out bytes.Buffer
		writeFontDiagnostics(&out)
		res.Detail = strings.TrimRight(out.String(), "\n")
	}
	return res
}

func doctorCheckStaleRuntime(_ context.Context, state *doctorRunState, _ string) doctorCheckResult {
	socketDir := state.deps.socketDir()
	entries, err := state.deps.readDir(socketDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return checkOK("stale-runtime", "global", "", socketDir+" not present")
		}
		return checkWarn("stale-runtime", "global", "", "could not scan "+socketDir, err.Error())
	}
	cutoff := state.deps.now().Add(-doctorStaleRuntimeAge)
	var stale []string
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(socketDir, name)
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if !isDoctorRuntimeArtifact(name, info.Mode()) || !info.ModTime().Before(cutoff) {
			continue
		}
		if info.Mode()&os.ModeSocket != 0 && socketAcceptsConnections(path) {
			continue
		}
		stale = append(stale, name)
	}
	if len(stale) == 0 {
		return checkOK("stale-runtime", "global", "", "no stale sockets or lock files older than 7 days")
	}
	sort.Strings(stale)
	return checkWarn(
		"stale-runtime",
		"global",
		"",
		fmt.Sprintf("%d orphan runtime entries older than 7 days", len(stale)),
		"Consider cleanup after confirming no matching server is alive: "+strings.Join(stale, ", "),
	)
}

func isDoctorRuntimeArtifact(name string, mode os.FileMode) bool {
	return mode&os.ModeSocket != 0 ||
		strings.HasSuffix(name, ".lock") ||
		strings.HasSuffix(name, ".start.lock")
}

func doctorCheckDiskPressure(_ context.Context, state *doctorRunState, _ string) doctorCheckResult {
	socketDir := state.deps.socketDir()
	usage, err := state.deps.diskUsage(socketDir)
	if err != nil {
		return checkWarn("disk-pressure", "global", "", "could not inspect "+socketDir+" disk usage", err.Error())
	}
	if usage.percent >= doctorDiskWarnPercent {
		return checkWarn(
			"disk-pressure",
			"global",
			"",
			fmt.Sprintf("runtime filesystem is %d%% full", usage.percent),
			fmt.Sprintf("Filesystem %d%% full; rotate or compress session log files in %s.", usage.percent, socketDir),
		)
	}
	return checkOK("disk-pressure", "global", "", fmt.Sprintf("runtime filesystem is %d%% full", usage.percent))
}

func doctorCheckServerReachable(ctx context.Context, state *doctorRunState, session string) doctorCheckResult {
	ctx, cancel := context.WithTimeout(ctx, doctorDefaultTimeout)
	defer cancel()
	if _, err := state.deps.runServerCommand(ctx, session, "list", []string{"--no-cwd"}); err != nil {
		return checkFail(
			"server-reachable",
			"session",
			session,
			"Server unresponsive: "+err.Error(),
			"Server unresponsive; see `amux _diag dump` for goroutine state.",
		)
	}
	return checkOK("server-reachable", "session", session, "server responded to bounded `amux list`")
}

func doctorCheckServerProcesses(ctx context.Context, state *doctorRunState, session string) doctorCheckResult {
	ctx, cancel := context.WithTimeout(ctx, doctorDefaultTimeout)
	defer cancel()
	processes, err := state.deps.listProcesses(ctx)
	if err != nil {
		return checkWarn("server-processes", "session", session, "could not inspect server processes", err.Error())
	}
	matches := serverProcessesForSession(processes, session)
	switch len(matches) {
	case 1:
		return checkOK("server-processes", "session", session, fmt.Sprintf("one `_server %s` process: pid %d", session, matches[0].pid))
	case 0:
		return checkFail("server-processes", "session", session, "no `_server "+session+"` process detected", "Expected exactly one `_server <session>` process for a live session.")
	default:
		pids := make([]string, 0, len(matches))
		for _, proc := range matches {
			pids = append(pids, strconv.Itoa(proc.pid))
		}
		return checkFail(
			"server-processes",
			"session",
			session,
			fmt.Sprintf("multiple `_server %s` processes detected: %s", session, strings.Join(pids, ", ")),
			"Multiple `_server <session>` processes detected; only the PID holding the live socket should remain.",
		)
	}
}

func doctorCheckSocket(ctx context.Context, state *doctorRunState, session string) doctorCheckResult {
	ctx, cancel := context.WithTimeout(ctx, doctorDefaultTimeout)
	defer cancel()

	mainSocket := state.deps.socketPath(session)
	pprofSocket := state.deps.pprofSocketPath(session)
	expectedPID := expectedServerPID(ctx, state, session)
	if err := verifyDoctorSocketFile(state, mainSocket); err != nil {
		return checkFail("socket", "session", session, "main socket is not healthy", err.Error())
	}
	if expectedPID > 0 {
		if err := verifyDoctorSocketOwner(ctx, state, mainSocket, expectedPID); err != nil {
			return checkFail("socket", "session", session, "main socket owner mismatch", err.Error())
		}
	}

	cfg, cfgErr := state.loadConfig()
	if cfgErr != nil {
		return checkFail("socket", "session", session, "cannot verify pprof socket because config failed to parse", cfgErr.Error())
	}
	if cfg.PprofEnabled() {
		if err := verifyDoctorSocketFile(state, pprofSocket); err != nil {
			return checkFail("socket", "session", session, "pprof socket is not healthy", err.Error())
		}
		if expectedPID > 0 {
			if err := verifyDoctorSocketOwner(ctx, state, pprofSocket, expectedPID); err != nil {
				return checkFail("socket", "session", session, "pprof socket owner mismatch", err.Error())
			}
		}
		return checkOK("socket", "session", session, "main and pprof sockets exist and are Unix sockets")
	}
	return checkOK("socket", "session", session, "main socket exists; pprof socket disabled by config")
}

func doctorCheckDiagInfo(ctx context.Context, state *doctorRunState, session string) doctorCheckResult {
	info := state.diagInfoForSession(ctx, session)
	if info.err != nil {
		return checkWarn("diag-info", "session", session, "_diag info unavailable", info.err.Error())
	}
	summary := fmt.Sprintf("pid %d, uptime %s, build %s, goroutines %d", info.info.PID, info.info.Uptime, info.info.Build, info.info.Goroutines)
	return checkOK("diag-info", "session", session, summary)
}

func doctorCheckGoroutines(ctx context.Context, state *doctorRunState, session string) doctorCheckResult {
	info := state.diagInfoForSession(ctx, session)
	if info.err != nil {
		return checkWarn("goroutines", "session", session, "goroutine summary unavailable", "Check `amux _diag goroutines` after enabling `[debug] pprof = true`.")
	}
	total := info.info.Goroutines
	states, err := doctorGoroutineHistogram(ctx, state, session)
	if err != nil {
		if total >= doctorGoroutineWarn {
			return checkWarn("goroutines", "session", session, fmt.Sprintf("%d goroutines", total), fmt.Sprintf("%d goroutines; check `amux _diag goroutines` for state histogram.", total))
		}
		return checkOK("goroutines", "session", session, fmt.Sprintf("%d goroutines; histogram unavailable: %v", total, err))
	}
	if total >= doctorGoroutineWarn {
		return checkWarn("goroutines", "session", session, fmt.Sprintf("%d goroutines", total), fmt.Sprintf("%d goroutines; check `amux _diag goroutines` for state histogram.", total))
	}
	if stateName, count := largestGoroutineState(states); count >= doctorGoroutineBucketWarn {
		return checkWarn("goroutines", "session", session, fmt.Sprintf("%d goroutines; %s=%d", total, stateName, count), fmt.Sprintf("%d goroutines in state %q; check `amux _diag goroutines`.", count, stateName))
	}
	return checkOK("goroutines", "session", session, fmt.Sprintf("%d goroutines", total))
}

func doctorGoroutineHistogram(ctx context.Context, state *doctorRunState, session string) (map[string]int, error) {
	ctx, cancel := context.WithTimeout(ctx, doctorDefaultTimeout)
	defer cancel()
	cfg, err := state.loadConfig()
	if err != nil {
		return nil, err
	}
	sockPath, err := state.deps.discoverPprof(ctx, session, cfg)
	if err != nil {
		return nil, err
	}
	body, err := state.deps.fetchEndpoint(debugEndpointRequest{
		sockPath:      sockPath,
		path:          "/debug/pprof/goroutine?debug=2",
		timeout:       doctorDefaultTimeout,
		configEnabled: true,
		missingHint:   doctorDiagMissingSocketHint(session),
	})
	if err != nil {
		return nil, err
	}
	_, states := summarizeGoroutineStates(body)
	return states, nil
}

func largestGoroutineState(states map[string]int) (string, int) {
	var name string
	var count int
	for state, n := range states {
		if n > count || (n == count && state < name) {
			name, count = state, n
		}
	}
	return name, count
}

func doctorDiagMissingSocketHint(session string) string {
	return fmt.Sprintf("pprof diagnostics socket %%s is not available for session %q; enable [debug] pprof = true in ~/.config/amux/config.toml and restart amux", session)
}

func doctorCheckClientsLog(ctx context.Context, state *doctorRunState, session string) doctorCheckResult {
	ctx, cancel := context.WithTimeout(ctx, doctorDefaultTimeout)
	defer cancel()
	out, err := state.deps.runServerCommand(ctx, session, "connection-log", nil)
	if err != nil {
		return checkWarn("clients-log", "session", session, "client log unavailable", err.Error())
	}
	flaps := clientReconnectCounts(out, state.deps.now())
	if len(flaps) == 0 {
		return checkOK("clients-log", "session", session, "no client flapping detected")
	}
	var parts []string
	for clientID, count := range flaps {
		parts = append(parts, fmt.Sprintf("%s=%d", clientID, count))
	}
	sort.Strings(parts)
	return checkWarn("clients-log", "session", session, "client reconnect flapping detected: "+strings.Join(parts, ", "), "See `amux log clients` for attach/detach history.")
}

func doctorCheckPanesLog(ctx context.Context, state *doctorRunState, session string) doctorCheckResult {
	ctx, cancel := context.WithTimeout(ctx, doctorDefaultTimeout)
	defer cancel()
	out, err := state.deps.runServerCommand(ctx, session, "pane-log", nil)
	if err != nil {
		return checkWarn("panes-log", "session", session, "pane log unavailable", err.Error())
	}
	count := abnormalPaneExitCount(out, state.deps.now())
	if count == 0 {
		return checkOK("panes-log", "session", session, "no recent abnormal pane exits")
	}
	return checkWarn("panes-log", "session", session, fmt.Sprintf("%d panes exited abnormally in the last 5 minutes", count), fmt.Sprintf("%d panes exited with non-zero status in the last 5 min; see `amux log panes`.", count))
}

func doctorCheckWatchdog(_ context.Context, state *doctorRunState, session string) doctorCheckResult {
	lines, err := recentWatchdogLogLines(state.deps.readFile, diagSessionLogPath(session), 100)
	if err != nil {
		return checkWarn("watchdog", "session", session, "watchdog log unavailable", err.Error())
	}
	recent := recentWatchdogCount(lines, state.deps.now())
	if recent == 0 {
		return checkOK("watchdog", "session", session, "no recent event_loop_watchdog entries")
	}
	return checkWarn("watchdog", "session", session, fmt.Sprintf("event_loop_watchdog fired %d times in 24h", recent), fmt.Sprintf("Watchdog fired %d times in 24h. Tail `%s` for command types and stacks.", recent, diagSessionLogPath(session)))
}

func doctorCheckBinary(ctx context.Context, state *doctorRunState, session string) doctorCheckResult {
	home, err := state.deps.userHomeDir()
	if err != nil {
		return checkWarn("binary", "session", session, "could not resolve home directory", err.Error())
	}
	installed := filepath.Join(home, ".local", "bin", "amux")
	installedInfo, err := state.deps.stat(installed)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return checkOK("binary", "session", session, installed+" not present; no installed binary to compare")
		}
		return checkWarn("binary", "session", session, "could not stat installed binary", err.Error())
	}
	info := state.diagInfoForSession(ctx, session)
	if info.err != nil {
		return checkWarn("binary", "session", session, "server binary unavailable", info.err.Error())
	}
	serverInfo, err := state.deps.stat(info.info.Binary)
	if err != nil {
		return checkWarn("binary", "session", session, "could not stat server binary "+info.info.Binary, err.Error())
	}
	if os.SameFile(serverInfo, installedInfo) {
		return checkOK("binary", "session", session, "server binary matches "+installed)
	}
	return checkWarn("binary", "session", session, "server binary differs from "+installed, "Server is running an older binary; `make install` triggers hot-reload.")
}

func writeDoctorReport(w io.Writer, report doctorReport, opts doctorOptions) {
	fmt.Fprintln(w, "amux doctor")
	fmt.Fprintf(w, "session: %s\n", report.Session)
	fmt.Fprintf(w, "overall: %s\n\n", report.Overall)

	for _, check := range report.Checks {
		if opts.quiet && check.Status == doctorStatusOK {
			continue
		}
		fmt.Fprintf(w, "[%s] %s: %s\n", check.Status, check.Name, check.Summary)
		if check.Hint != "" {
			fmt.Fprintf(w, "      hint: %s\n", check.Hint)
		}
		if opts.verbose && check.Detail != "" {
			for _, line := range strings.Split(check.Detail, "\n") {
				fmt.Fprintf(w, "      %s\n", line)
			}
		}
	}
}

func maxDoctorStatus(a, b doctorStatus) doctorStatus {
	if doctorStatusRank(b) > doctorStatusRank(a) {
		return b
	}
	return a
}

func doctorStatusRank(status doctorStatus) int {
	switch status {
	case doctorStatusFail:
		return 2
	case doctorStatusWarn:
		return 1
	default:
		return 0
	}
}

func doctorExitCode(status doctorStatus) int {
	switch status {
	case doctorStatusFail:
		return 2
	case doctorStatusWarn:
		return 1
	default:
		return 0
	}
}

func discoverDoctorSessions(deps doctorDeps) ([]string, error) {
	entries, err := deps.readDir(deps.socketDir())
	if err != nil {
		return nil, err
	}
	var sessions []string
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			continue
		}
		name := entry.Name()
		if strings.Contains(name, ".") {
			continue
		}
		sessions = append(sessions, name)
	}
	sort.Strings(sessions)
	return sessions, nil
}

func runDoctorServerCommand(ctx context.Context, sessionName, cmdName string, args []string) (string, error) {
	var out bytes.Buffer
	if err := runDoctorServerCommandWithIO(ctx, &out, sessionName, cmdName, args); err != nil {
		return "", err
	}
	return out.String(), nil
}

func runDoctorServerCommandWithIO(ctx context.Context, w io.Writer, sessionName, cmdName string, args []string) error {
	conn, err := dialutil.DialUnixContext(ctx, server.SocketPath(sessionName))
	if err != nil {
		return fmt.Errorf("connecting to server: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	socket := &serverSocket{
		conn:   conn,
		reader: server.NewReader(conn),
		writer: server.NewWriter(conn),
	}
	if err := socket.writer.WriteMsg(newCommandMessage(cmdName, args)); err != nil {
		return err
	}
	for {
		reply, err := socket.reader.ReadMsg()
		if err != nil {
			return err
		}
		if reply.Type != server.MsgTypeCmdResult {
			continue
		}
		if reply.CmdErr != "" {
			return fmt.Errorf("%s", reply.CmdErr)
		}
		_, err = io.WriteString(w, reply.CmdOutput)
		return err
	}
}

func listDoctorProcesses(ctx context.Context) ([]doctorProcess, error) {
	out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return nil, err
	}
	var processes []doctorProcess
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		command := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		processes = append(processes, doctorProcess{pid: pid, command: command})
	}
	return processes, nil
}

func serverProcessesForSession(processes []doctorProcess, session string) []doctorProcess {
	var matches []doctorProcess
	for _, proc := range processes {
		if commandRunsServerSession(proc.command, session) {
			matches = append(matches, proc)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].pid < matches[j].pid })
	return matches
}

func commandRunsServerSession(command, session string) bool {
	fields := strings.Fields(command)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "_server" && fields[i+1] == session {
			return true
		}
	}
	return false
}

func expectedServerPID(ctx context.Context, state *doctorRunState, session string) int {
	info := state.diagInfoForSession(ctx, session)
	if info.err == nil && info.info.PID > 0 {
		return info.info.PID
	}
	processes, err := state.deps.listProcesses(ctx)
	if err != nil {
		return 0
	}
	matches := serverProcessesForSession(processes, session)
	if len(matches) == 1 {
		return matches[0].pid
	}
	return 0
}

func verifyDoctorSocketFile(state *doctorRunState, path string) error {
	info, err := state.deps.lstat(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s exists but is not a Unix socket", path)
	}
	return nil
}

func verifyDoctorSocketOwner(ctx context.Context, state *doctorRunState, path string, expectedPID int) error {
	out, err := state.deps.runSS(ctx)
	if err != nil {
		return nil
	}
	pid, ok := socketOwnerPIDFromSS(out, path)
	if !ok {
		return fmt.Errorf("%s is not listed by `ss -lxp`", path)
	}
	if pid != expectedPID {
		return fmt.Errorf("%s is bound by pid %d, expected pid %d", path, pid, expectedPID)
	}
	return nil
}

func socketOwnerPIDFromSS(output []byte, path string) (int, bool) {
	for _, lineBytes := range bytes.Split(output, []byte{'\n'}) {
		line := string(lineBytes)
		if ssLineSocketPath(line) != path {
			continue
		}
		pid, err := strconv.Atoi(ssLinePID(line))
		if err != nil || pid <= 0 {
			return 0, false
		}
		return pid, true
	}
	return 0, false
}

func socketAcceptsConnections(path string) bool {
	conn, err := dialutil.DialUnixStaleProbe(path)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func doctorDiskUsageForPath(path string) (doctorDiskUsage, error) {
	target := path
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		target = filepath.Dir(target)
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(target, &stat); err != nil {
		return doctorDiskUsage{}, err
	}
	if stat.Blocks == 0 {
		return doctorDiskUsage{}, nil
	}
	used := stat.Blocks - stat.Bavail
	percent := int((used * 100) / stat.Blocks)
	return doctorDiskUsage{percent: percent}, nil
}

func clientReconnectCounts(output string, now time.Time) map[string]int {
	counts := make(map[string]int)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] == "TS" || fields[0] == "No" {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, fields[0])
		if err != nil || now.Sub(ts) > doctorRecentClientWindow || now.Before(ts) {
			continue
		}
		if fields[1] == "attach" {
			counts[fields[2]]++
		}
	}
	for clientID, count := range counts {
		if count <= doctorClientFlapWarn {
			delete(counts, clientID)
		}
	}
	return counts
}

func abnormalPaneExitCount(output string, now time.Time) int {
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 || fields[0] == "TS" || fields[0] == "No" {
			continue
		}
		if fields[1] != "exit" {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, fields[0])
		if err != nil || now.Sub(ts) > doctorRecentPaneExitWindow || now.Before(ts) {
			continue
		}
		reason := strings.Join(fields[7:], " ")
		if reason != "-" && reason != "exit 0" {
			count++
		}
	}
	return count
}

func recentWatchdogCount(lines []string, now time.Time) int {
	count := 0
	for _, line := range lines {
		if !strings.Contains(line, "event_loop_watchdog") {
			continue
		}
		ts, ok := watchdogLineTime(line)
		if ok && (now.Sub(ts) > doctorWatchdogWindow || now.Before(ts)) {
			continue
		}
		count++
	}
	return count
}

func watchdogLineTime(line string) (time.Time, bool) {
	var fields map[string]any
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		return time.Time{}, false
	}
	for _, key := range []string{"timestamp", "ts", "time"} {
		raw, ok := fields[key].(string)
		if !ok || raw == "" {
			continue
		}
		for _, layout := range []string{time.RFC3339Nano, "2006/01/02 15:04:05"} {
			if ts, err := time.Parse(layout, raw); err == nil {
				return ts, true
			}
		}
	}
	return time.Time{}, false
}
