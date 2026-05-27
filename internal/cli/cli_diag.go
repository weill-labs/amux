package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/server"
)

type diagDeps struct {
	loadConfig     debugConfigLoader
	discoverSocket func(context.Context, string, *config.Config) (string, error)
	fetch          func(debugEndpointRequest) ([]byte, error)
	writeFile      func(string, []byte, fs.FileMode) error
	readFile       func(string) ([]byte, error)
	logPath        func(string) string
	stderr         io.Writer
}

func runDiagCommand(sessionName string, args []string) {
	if err := runDiagCommandWithDeps(context.Background(), os.Stdout, sessionName, args, diagDeps{stderr: os.Stderr}); err != nil {
		fmt.Fprintf(os.Stderr, "amux _diag: %v\n", err)
		os.Exit(1)
	}
}

func runDiagCommandWithIO(ctx context.Context, w io.Writer, sessionName string, args []string) error {
	return runDiagCommandWithDeps(ctx, w, sessionName, args, diagDeps{})
}

func runDiagCommandWithDeps(ctx context.Context, w io.Writer, sessionName string, args []string, deps diagDeps) error {
	debugArgs, warning, err := parseDiagCompatCommand(args)
	if err != nil {
		return err
	}
	deps = fillDiagDeps(deps)
	if warning != "" && deps.stderr != nil {
		if _, err := fmt.Fprintln(deps.stderr, warning); err != nil {
			return err
		}
	}
	return runDebugCommandWithFullDeps(ctx, w, sessionName, debugArgs, debugDeps{
		loadConfig:     deps.loadConfig,
		discoverSocket: deps.discoverSocket,
		fetch:          deps.fetch,
		writeFile:      deps.writeFile,
		readFile:       deps.readFile,
		logPath:        deps.logPath,
		diagCompat:     true,
	})
}

func fillDiagDeps(deps diagDeps) diagDeps {
	if deps.loadConfig == nil {
		deps.loadConfig = loadDebugConfig
	}
	if deps.discoverSocket == nil {
		deps.discoverSocket = discoverDiagPprofSocket
	}
	if deps.fetch == nil {
		deps.fetch = fetchDebugEndpoint
	}
	if deps.writeFile == nil {
		deps.writeFile = os.WriteFile
	}
	if deps.readFile == nil {
		deps.readFile = os.ReadFile
	}
	if deps.logPath == nil {
		deps.logPath = diagSessionLogPath
	}
	if deps.stderr == nil {
		deps.stderr = io.Discard
	}
	return deps
}

func parseDiagCompatCommand(args []string) ([]string, string, error) {
	if len(args) == 0 {
		args = []string{"dump"}
	}

	switch args[0] {
	case "dump":
		if len(args) != 1 {
			return nil, "", errors.New(diagUsage)
		}
		return []string{"dump"}, `amux _diag dump is deprecated; use "amux debug dump"`, nil
	case "goroutines":
		if len(args) != 1 {
			return nil, "", errors.New(diagUsage)
		}
		return []string{"goroutines", "--summary"}, `amux _diag goroutines is deprecated; use "amux debug goroutines --summary"`, nil
	case "heap":
		outputPath, err := parseDiagOutputFlag(args[1:])
		if err != nil {
			return nil, "", err
		}
		debugArgs := []string{"heap", "--raw"}
		if outputPath != "" {
			debugArgs = append(debugArgs, "--output", outputPath)
		}
		return debugArgs, `amux _diag heap is deprecated; use "amux debug heap --raw"`, nil
	case "info":
		if len(args) != 1 {
			return nil, "", errors.New(diagUsage)
		}
		return []string{"info"}, `amux _diag info is deprecated; use "amux debug info"`, nil
	default:
		return nil, "", errors.New(diagUsage)
	}
}

func parseDiagOutputFlag(args []string) (string, error) {
	switch len(args) {
	case 0:
		return "", nil
	case 2:
		if args[0] != "--output" || args[1] == "" {
			return "", errors.New(diagUsage)
		}
		return args[1], nil
	default:
		return "", errors.New(diagUsage)
	}
}

func writeGoroutineSummary(w io.Writer, dump []byte) error {
	count, states := summarizeGoroutineStates(dump)
	if _, err := fmt.Fprintf(w, "goroutines: %d\nstates:\n", count); err != nil {
		return err
	}
	names := make([]string, 0, len(states))
	for name := range states {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, err := fmt.Fprintf(w, "  %s: %d\n", name, states[name]); err != nil {
			return err
		}
	}
	return nil
}

func summarizeGoroutineStates(dump []byte) (int, map[string]int) {
	states := make(map[string]int)
	for _, rawLine := range strings.Split(string(dump), "\n") {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, "goroutine ") {
			continue
		}
		start := strings.Index(line, "[")
		end := strings.Index(line, "]")
		if start < 0 || end <= start {
			continue
		}
		state := strings.TrimSpace(line[start+1 : end])
		if comma := strings.Index(state, ","); comma >= 0 {
			state = strings.TrimSpace(state[:comma])
		}
		if state == "" {
			state = "unknown"
		}
		states[state]++
	}
	count := 0
	for _, n := range states {
		count += n
	}
	return count, states
}

func writeDiagInfo(w io.Writer, sessionName string, body []byte, deps diagDeps) error {
	var info server.DiagInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return fmt.Errorf("decoding info: %w", err)
	}
	if _, err := fmt.Fprintf(w, "pid: %d\nuptime: %s\nbinary: %s\nbuild: %s\ngo: %s\ngoroutines: %d\n",
		info.PID, info.Uptime, info.Binary, info.Build, info.GoVersion, info.Goroutines); err != nil {
		return err
	}
	lines, err := recentWatchdogLogLines(deps.readFile, deps.logPath(sessionName), 10)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "recent event_loop_watchdog log lines:"); err != nil {
		return err
	}
	if len(lines) == 0 {
		_, err := fmt.Fprintln(w, "(none)")
		return err
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func recentWatchdogLogLines(readFile func(string) ([]byte, error), path string, limit int) ([]string, error) {
	data, err := readFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading watchdog log %s: %w", path, err)
	}
	lines := make([]string, 0)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "event_loop_watchdog") {
			lines = append(lines, line)
		}
	}
	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return lines, nil
}

func diagSessionLogPath(sessionName string) string {
	logDir := os.Getenv("AMUX_LOG_DIR")
	if logDir == "" {
		logDir = server.SocketDir()
	}
	return filepath.Join(logDir, sessionName+".log")
}

func diagMissingSocketHint(sessionName string) string {
	return fmt.Sprintf("pprof diagnostics socket %%s is not available for session %q; enable [debug] pprof = true in ~/.config/amux/config.toml and restart amux", sessionName)
}

type diagUnavailableError struct {
	sessionName string
	cause       error
}

func (e *diagUnavailableError) Error() string {
	msg := fmt.Sprintf("pprof diagnostics socket is not available for session %q; enable [debug] pprof = true in ~/.config/amux/config.toml and restart amux", e.sessionName)
	if e.cause == nil {
		return msg
	}
	return msg + ": " + e.cause.Error()
}

func (e *diagUnavailableError) Unwrap() error {
	return e.cause
}

func diagPprofUnavailableError(sessionName string, cause error) error {
	return &diagUnavailableError{sessionName: sessionName, cause: cause}
}

type diagDiscoveryDeps struct {
	serverSocketPath func(string) string
	pprofSocketPath  func(string) string
	runSS            func(context.Context) ([]byte, error)
	glob             func(string) ([]string, error)
	probe            func(context.Context, string) error
}

func discoverDiagPprofSocket(ctx context.Context, sessionName string, cfg *config.Config) (string, error) {
	return discoverDiagPprofSocketWithDeps(ctx, sessionName, cfg, diagDiscoveryDeps{})
}

func discoverDiagPprofSocketWithDeps(ctx context.Context, sessionName string, cfg *config.Config, deps diagDiscoveryDeps) (string, error) {
	deps = fillDiagDiscoveryDeps(deps)
	mainSocket := deps.serverSocketPath(sessionName)
	directPprofSocket := deps.pprofSocketPath(sessionName)

	if ssOutput, err := deps.runSS(ctx); err == nil {
		if sockPath := pprofSocketFromSS(ssOutput, mainSocket); sockPath != "" {
			if err := deps.probe(ctx, sockPath); err == nil {
				return sockPath, nil
			}
		}
	}

	for _, candidate := range fallbackDiagPprofCandidates(directPprofSocket, deps.glob) {
		if err := deps.probe(ctx, candidate); err == nil {
			return candidate, nil
		}
	}
	return "", diagPprofUnavailableError(sessionName, os.ErrNotExist)
}

func fillDiagDiscoveryDeps(deps diagDiscoveryDeps) diagDiscoveryDeps {
	if deps.serverSocketPath == nil {
		deps.serverSocketPath = server.SocketPath
	}
	if deps.pprofSocketPath == nil {
		deps.pprofSocketPath = server.PprofSocketPath
	}
	if deps.runSS == nil {
		deps.runSS = runSSUnixListeners
	}
	if deps.glob == nil {
		deps.glob = filepath.Glob
	}
	if deps.probe == nil {
		deps.probe = probeDiagPprofSocket
	}
	return deps
}

func runSSUnixListeners(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "ss", "-lxp").Output()
}

func pprofSocketFromSS(output []byte, mainSocket string) string {
	lines := bytes.Split(output, []byte{'\n'})
	pid := ""
	for _, lineBytes := range lines {
		line := string(lineBytes)
		if ssLineSocketPath(line) != mainSocket {
			continue
		}
		pid = ssLinePID(line)
		if pid != "" {
			break
		}
	}
	if pid == "" {
		return ""
	}
	for _, lineBytes := range lines {
		line := string(lineBytes)
		if !strings.Contains(line, ".pprof") || !strings.Contains(line, "pid="+pid) {
			continue
		}
		if path := ssLineSocketPath(line); strings.HasSuffix(path, ".pprof") {
			return path
		}
	}
	return ""
}

func ssLinePID(line string) string {
	idx := strings.Index(line, "pid=")
	if idx < 0 {
		return ""
	}
	start := idx + len("pid=")
	end := start
	for end < len(line) && line[end] >= '0' && line[end] <= '9' {
		end++
	}
	return line[start:end]
}

func ssLineSocketPath(line string) string {
	for _, field := range strings.Fields(line) {
		if strings.HasPrefix(field, "/") {
			return field
		}
	}
	return ""
}

func fallbackDiagPprofCandidates(directPprofSocket string, glob func(string) ([]string, error)) []string {
	candidates := []string{directPprofSocket}
	matches, err := glob(filepath.Join(filepath.Dir(directPprofSocket), "*.pprof"))
	if err == nil {
		directBase := filepath.Base(directPprofSocket)
		for _, match := range matches {
			if filepath.Base(match) == directBase {
				candidates = append(candidates, match)
			}
		}
	}
	sort.Strings(candidates)
	return compactStrings(candidates)
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:0]
	for _, value := range values {
		if value == "" {
			continue
		}
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}

func probeDiagPprofSocket(ctx context.Context, sockPath string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sockPath)
		},
	}
	defer transport.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://amux/debug/pprof/", nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pprof probe returned %s", resp.Status)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 32*1024))
	return nil
}
