package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/weill-labs/amux/internal/client"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/server"
)

const debugDisabledHint = "pprof debug endpoint is disabled; set [debug].pprof = true in config.toml and restart amux"

type debugConfigLoader func() (*config.Config, error)
type debugServerCommandRunner func(io.Writer, string, string, []string) error

type debugResponseKind int

const (
	debugResponseGoroutineSummary debugResponseKind = iota + 1
	debugResponseInfo
)

type debugDeps struct {
	loadConfig       debugConfigLoader
	runServerCommand debugServerCommandRunner
	discoverSocket   func(context.Context, string, *config.Config) (string, error)
	fetch            func(debugEndpointRequest) ([]byte, error)
	writeFile        func(string, []byte, fs.FileMode) error
	readFile         func(string) ([]byte, error)
	logPath          func(string) string
	diagCompat       bool
}

func runDebugCommand(sessionName string, args []string) {
	if err := runDebugCommandWithIO(os.Stdout, sessionName, args); err != nil {
		fmt.Fprintf(os.Stderr, "amux debug: %v\n", err)
		os.Exit(1)
	}
}

func runDebugCommandWithIO(w io.Writer, sessionName string, args []string) error {
	return runDebugCommandWithConfigLoader(w, sessionName, args, loadDebugConfig)
}

func runDebugCommandWithConfigLoader(w io.Writer, sessionName string, args []string, loadConfig debugConfigLoader) error {
	return runDebugCommandWithDeps(w, sessionName, args, loadConfig, runServerCommandWithIO)
}

func runDebugCommandWithDeps(w io.Writer, sessionName string, args []string, loadConfig debugConfigLoader, runServerCommand debugServerCommandRunner) error {
	return runDebugCommandWithFullDeps(context.Background(), w, sessionName, args, debugDeps{
		loadConfig:       loadConfig,
		runServerCommand: runServerCommand,
	})
}

func runDebugCommandWithFullDeps(ctx context.Context, w io.Writer, sessionName string, args []string, deps debugDeps) error {
	deps = fillDebugDeps(deps)
	endpoint, err := parseDebugCommandWithConfigLoader(sessionName, args, deps.loadConfig)
	if err != nil {
		return err
	}
	if endpoint.serverCommand != "" {
		return deps.runServerCommand(w, sessionName, endpoint.serverCommand, nil)
	}
	if endpoint.discoverSocket {
		if !deps.diagCompat && !endpoint.configEnabled {
			if err := ensureDebugSocketAvailable(endpoint); err != nil {
				return err
			}
		}
		sockPath, err := deps.discoverSocket(ctx, sessionName, endpoint.config)
		if err != nil {
			var unavailable *diagUnavailableError
			if errors.As(err, &unavailable) {
				return err
			}
			return diagPprofUnavailableError(sessionName, err)
		}
		endpoint.sockPath = sockPath
	}
	if endpoint.path == "" {
		if err := ensureDebugSocketAvailable(endpoint); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w, endpoint.sockPath)
		return err
	}

	body, err := deps.fetch(endpoint)
	if err != nil {
		return err
	}
	switch endpoint.responseKind {
	case debugResponseGoroutineSummary:
		return writeGoroutineSummary(w, body)
	case debugResponseInfo:
		return writeDiagInfo(w, sessionName, body, diagDeps{
			readFile: deps.readFile,
			logPath:  deps.logPath,
		})
	}
	if endpoint.outputPath != "" {
		return deps.writeFile(endpoint.outputPath, body, 0600)
	}
	_, err = w.Write(body)
	return err
}

func fillDebugDeps(deps debugDeps) debugDeps {
	if deps.loadConfig == nil {
		deps.loadConfig = loadDebugConfig
	}
	if deps.runServerCommand == nil {
		deps.runServerCommand = runServerCommandWithIO
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
	return deps
}

type debugEndpointRequest struct {
	sockPath       string
	path           string
	serverCommand  string
	timeout        time.Duration
	configEnabled  bool
	missingHint    string
	discoverSocket bool
	config         *config.Config
	responseKind   debugResponseKind
	outputPath     string
}

func (req *debugEndpointRequest) useClientPprof(sessionName string) {
	req.sockPath = client.PprofSocketPath(sessionName)
	req.missingHint = "pprof debug socket %s is not available; attach or restart a client after enabling [debug].pprof"
	req.discoverSocket = false
}

func loadDebugConfig() (*config.Config, error) {
	return config.Load(config.DefaultPath())
}

func parseDebugCommandWithConfigLoader(sessionName string, args []string, loadConfig debugConfigLoader) (debugEndpointRequest, error) {
	if len(args) == 0 {
		return debugEndpointRequest{}, errors.New(debugUsage)
	}

	if args[0] == "frames" {
		if len(args) != 1 {
			return debugEndpointRequest{}, errors.New(debugUsage)
		}
		return debugEndpointRequest{
			sockPath:      server.PprofSocketPath(sessionName),
			serverCommand: "debug-frames",
			timeout:       5 * time.Second,
		}, nil
	}
	if args[0] == "scrollback" {
		if len(args) != 1 {
			return debugEndpointRequest{}, errors.New(debugUsage)
		}
		return debugEndpointRequest{
			sockPath:      server.PprofSocketPath(sessionName),
			serverCommand: "debug-scrollback",
			timeout:       5 * time.Second,
		}, nil
	}

	cfg, err := loadConfig()
	if err != nil {
		return debugEndpointRequest{}, fmt.Errorf("loading config: %w", err)
	}
	req := debugEndpointRequest{
		sockPath:       server.PprofSocketPath(sessionName),
		timeout:        5 * time.Second,
		configEnabled:  cfg.PprofEnabled(),
		missingHint:    "pprof debug socket %s is not available; restart the server after enabling [debug].pprof",
		discoverSocket: true,
		config:         cfg,
	}

	switch args[0] {
	case "dump":
		if len(args) != 1 {
			return debugEndpointRequest{}, errors.New(debugUsage)
		}
		req.path = "/debug/pprof/goroutine?debug=2"
	case "goroutines":
		summary, err := parseDebugGoroutinesArgs(args[1:])
		if err != nil {
			return debugEndpointRequest{}, errors.New(debugUsage)
		}
		req.path = "/debug/pprof/goroutine?debug=2"
		if summary {
			req.responseKind = debugResponseGoroutineSummary
		}
	case "heap":
		raw, outputPath, err := parseDebugHeapArgs(args[1:])
		if err != nil {
			return debugEndpointRequest{}, errors.New(debugUsage)
		}
		if raw {
			req.path = "/debug/pprof/heap?gc=1"
			req.outputPath = outputPath
		} else {
			req.path = "/debug/pprof/heap?debug=1"
		}
	case "info":
		if len(args) != 1 {
			return debugEndpointRequest{}, errors.New(debugUsage)
		}
		req.path = "/debug/amux/info"
		req.responseKind = debugResponseInfo
	case "client-goroutines":
		if len(args) != 1 {
			return debugEndpointRequest{}, errors.New(debugUsage)
		}
		req.useClientPprof(sessionName)
		req.path = "/debug/pprof/goroutine?debug=2"
	case "client-heap":
		if len(args) != 1 {
			return debugEndpointRequest{}, errors.New(debugUsage)
		}
		req.useClientPprof(sessionName)
		req.path = "/debug/pprof/heap?debug=1"
	case "socket":
		if len(args) != 1 {
			return debugEndpointRequest{}, errors.New(debugUsage)
		}
		req.discoverSocket = false
	case "profile":
		duration, parseErr := parseDebugProfileDuration(args[1:])
		if parseErr != nil {
			return debugEndpointRequest{}, parseErr
		}
		seconds := int(math.Ceil(duration.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		req.timeout = duration + 5*time.Second
		req.path = "/debug/pprof/profile?seconds=" + strconv.Itoa(seconds)
	case "client-profile":
		duration, parseErr := parseDebugProfileDuration(args[1:])
		if parseErr != nil {
			return debugEndpointRequest{}, parseErr
		}
		seconds := int(math.Ceil(duration.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		req.useClientPprof(sessionName)
		req.timeout = duration + 5*time.Second
		req.path = "/debug/pprof/profile?seconds=" + strconv.Itoa(seconds)
	default:
		return debugEndpointRequest{}, errors.New(debugUsage)
	}

	return req, nil
}

func parseDebugGoroutinesArgs(args []string) (bool, error) {
	switch len(args) {
	case 0:
		return false, nil
	case 1:
		if args[0] == "--summary" {
			return true, nil
		}
	}
	return false, errors.New(debugUsage)
}

func parseDebugHeapArgs(args []string) (bool, string, error) {
	if len(args) == 0 {
		return false, "", nil
	}
	if args[0] != "--raw" {
		return false, "", errors.New(debugUsage)
	}
	outputPath, err := parseDiagOutputFlag(args[1:])
	if err != nil {
		return false, "", err
	}
	return true, outputPath, nil
}

func parseDebugProfileDuration(args []string) (time.Duration, error) {
	duration := 30 * time.Second
	switch len(args) {
	case 0:
	case 2:
		if args[0] != "--duration" {
			return 0, errors.New(debugUsage)
		}
		seconds, err := strconv.Atoi(args[1])
		var parsed time.Duration
		if err == nil {
			parsed = time.Duration(seconds) * time.Second
		} else {
			parsed, err = time.ParseDuration(args[1])
		}
		if err != nil || parsed <= 0 {
			return 0, fmt.Errorf("invalid profile duration %q", args[1])
		}
		duration = parsed
	default:
		return 0, errors.New(debugUsage)
	}
	return duration, nil
}

func ensureDebugSocketAvailable(req debugEndpointRequest) error {
	if _, err := os.Stat(req.sockPath); err != nil {
		if os.IsNotExist(err) {
			if !req.configEnabled {
				return errors.New(debugDisabledHint)
			}
			missingHint := req.missingHint
			if missingHint == "" {
				missingHint = "pprof debug socket %s is not available; restart the server after enabling [debug].pprof"
			}
			return fmt.Errorf(missingHint, req.sockPath)
		}
		return fmt.Errorf("stat pprof debug socket: %w", err)
	}
	return nil
}

func fetchDebugEndpoint(req debugEndpointRequest) ([]byte, error) {
	if err := ensureDebugSocketAvailable(req); err != nil {
		return nil, err
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", req.sockPath)
		},
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{Transport: transport, Timeout: req.timeout}
	resp, err := client.Get("http://amux" + req.path)
	if err != nil {
		return nil, fmt.Errorf("requesting %s: %w", req.path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s returned %s: %s", req.path, resp.Status, body)
	}
	return io.ReadAll(resp.Body)
}
