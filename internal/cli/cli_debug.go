package cli

import (
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/server"
)

const debugDisabledHint = "pprof debug endpoint is disabled; set [debug].pprof = true in config.toml and restart the server"

type debugConfigLoader func() (*config.Config, error)

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
	endpoint, err := parseDebugCommandWithConfigLoader(sessionName, args, loadConfig)
	if err != nil {
		return err
	}
	if endpoint.path == "" {
		if err := ensureDebugSocketAvailable(endpoint); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w, endpoint.sockPath)
		return err
	}

	body, err := fetchDebugEndpoint(endpoint)
	if err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

type debugEndpointRequest struct {
	sockPath      string
	path          string
	timeout       time.Duration
	configEnabled bool
}

func parseDebugCommand(sessionName string, args []string) (debugEndpointRequest, error) {
	return parseDebugCommandWithConfigLoader(sessionName, args, loadDebugConfig)
}

func loadDebugConfig() (*config.Config, error) {
	return config.Load(config.DefaultPath())
}

func parseDebugCommandWithConfigLoader(sessionName string, args []string, loadConfig debugConfigLoader) (debugEndpointRequest, error) {
	if len(args) == 0 {
		return debugEndpointRequest{}, fmt.Errorf(debugUsage)
	}

	cfg, err := loadConfig()
	if err != nil {
		return debugEndpointRequest{}, fmt.Errorf("loading config: %w", err)
	}
	req := debugEndpointRequest{
		sockPath:      server.PprofSocketPath(sessionName),
		timeout:       5 * time.Second,
		configEnabled: cfg.PprofEnabled(),
	}

	switch args[0] {
	case "goroutines":
		if len(args) != 1 {
			return debugEndpointRequest{}, fmt.Errorf(debugUsage)
		}
		req.path = "/debug/pprof/goroutine?debug=2"
	case "heap":
		if len(args) != 1 {
			return debugEndpointRequest{}, fmt.Errorf(debugUsage)
		}
		req.path = "/debug/pprof/heap?debug=1"
	case "socket":
		if len(args) != 1 {
			return debugEndpointRequest{}, fmt.Errorf(debugUsage)
		}
	case "profile":
		duration := 30 * time.Second
		switch len(args) {
		case 1:
		case 3:
			if args[1] != "--duration" {
				return debugEndpointRequest{}, fmt.Errorf(debugUsage)
			}
			duration, err = time.ParseDuration(args[2])
			if err != nil || duration <= 0 {
				return debugEndpointRequest{}, fmt.Errorf("invalid profile duration %q", args[2])
			}
		default:
			return debugEndpointRequest{}, fmt.Errorf(debugUsage)
		}
		seconds := int(math.Ceil(duration.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		req.timeout = duration + 5*time.Second
		req.path = "/debug/pprof/profile?seconds=" + strconv.Itoa(seconds)
	default:
		return debugEndpointRequest{}, fmt.Errorf(debugUsage)
	}

	return req, nil
}

func ensureDebugSocketAvailable(req debugEndpointRequest) error {
	if _, err := os.Stat(req.sockPath); err != nil {
		if os.IsNotExist(err) {
			if !req.configEnabled {
				return fmt.Errorf(debugDisabledHint)
			}
			return fmt.Errorf("pprof debug socket %s is not available; restart the server after enabling [debug].pprof", req.sockPath)
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
