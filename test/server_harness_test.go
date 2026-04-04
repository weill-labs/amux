package test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/server"
)

// ServerHarness starts the inner amux server daemon and attaches a headless
// client. The client maintains local emulators and responds to capture
// requests — making it the rendering source of truth, same as a real client.
// Harness commands are synchronous: after runCmd("split") returns, capture()
// immediately reflects the split. Zero polling, zero time.Sleep.
type ServerHarness struct {
	tb                testing.TB
	session           string
	cmd               *exec.Cmd
	home              string
	cols              int
	rows              int
	coverDir          string // per-test GOCOVERDIR subdirectory (avoids coverage metadata races)
	extraEnv          []string
	logPath           string
	exitUnattached    bool
	ownsProcessGroup  bool
	client            *headlessClient // attached headless client for capture
	keepalive         *headlessClient // secondary client for persistent harnesses
	shutdownPipe      *os.File
	waitDone          chan struct{}
	waitOnce          sync.Once
	waitMu            sync.Mutex
	waitErr           error
	commandConnMu     sync.Mutex
	lastCommandConn   net.Conn
	awaitingReconnect bool
	postReloadProbes  bool

	diagMu         sync.Mutex
	currentWait    string
	currentCommand string
}

var harnessBlockedEnvKeys = map[string]struct{}{
	"AMUX_PANE":    {},
	"AMUX_SESSION": {},
	"TMUX":         {},
}

const harnessCommandWaitDelay = 250 * time.Millisecond

// newServerHarnessWithSize starts a server harness with a custom terminal size.
func newServerHarnessWithSize(tb testing.TB, cols, rows int) *ServerHarness {
	return newServerHarnessWithConfig(tb, cols, rows, "")
}

// newServerHarness starts a server daemon with a unique session name,
// waits for the ready signal, and seeds the first pane. Safe for parallel tests.
func newServerHarness(tb testing.TB) *ServerHarness {
	tb.Helper()
	return newServerHarnessWithOptions(tb, 80, 24, "", false, false)
}

// newServerHarnessPersistent starts a server that does NOT exit when all
// clients disconnect. Used by tests that deliberately detach all clients
// and then issue commands against the still-running server.
func newServerHarnessPersistent(tb testing.TB) *ServerHarness {
	tb.Helper()
	return newServerHarnessWithOptions(tb, 80, 24, "", false, false)
}

// newServerHarnessPersistentKeepalive is like newServerHarnessPersistent but
// keeps a second headless client attached so capture forwarding survives if the
// primary test client briefly disconnects.
func newServerHarnessPersistentKeepalive(tb testing.TB) *ServerHarness {
	tb.Helper()
	return newServerHarnessWithOptions(tb, 80, 24, "", false, true)
}

// newServerHarnessWithConfig starts a server with a custom config file.
// The config is written to a temp file and passed via AMUX_CONFIG.
// Pass an empty configContent to start with the default (no) config.
// The default harness keeps the server alive across transient client gaps;
// tests that specifically exercise exit-on-unattached should call
// newServerHarnessWithOptions(..., true) explicitly.
func newServerHarnessWithConfig(tb testing.TB, cols, rows int, configContent string) *ServerHarness {
	tb.Helper()
	return newServerHarnessWithOptions(tb, cols, rows, configContent, false, false)
}

// newServerHarnessExitUnattached starts a server that exits when all clients
// disconnect. Use this only in tests that explicitly exercise exit-unattached.
func newServerHarnessExitUnattached(tb testing.TB) *ServerHarness {
	tb.Helper()
	return newServerHarnessWithOptions(tb, 80, 24, "", true, false)
}

// newServerHarnessWithOptions is the shared constructor. When exitUnattached
// is true the server self-terminates after all clients disconnect.
func newServerHarnessWithOptions(tb testing.TB, cols, rows int, configContent string, exitUnattached, keepalive bool, extraEnv ...string) *ServerHarness {
	tb.Helper()
	return newServerHarnessForSession(tb, "", "", cols, rows, configContent, exitUnattached, keepalive, extraEnv...)
}

func newServerHarnessForSession(tb testing.TB, session, home string, cols, rows int, configContent string, exitUnattached, keepalive bool, extraEnv ...string) *ServerHarness {
	tb.Helper()
	var b [4]byte
	if session == "" {
		rand.Read(b[:])
		session = fmt.Sprintf("t-%x", b)
	}

	// Create pipes for deterministic startup and clean-shutdown signals.
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		tb.Fatalf("creating ready pipe: %v", err)
	}
	shutdownReadPipe, shutdownWritePipe, err := os.Pipe()
	if err != nil {
		readPipe.Close()
		writePipe.Close()
		tb.Fatalf("creating shutdown pipe: %v", err)
	}

	cmd := exec.Command(amuxBin, "_server", session)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.ExtraFiles = []*os.File{writePipe, shutdownWritePipe} // fds 3 and 4 in child
	if home == "" {
		home = newTestHome(tb)
	}
	env := removeEnv(os.Environ(), "AMUX_EXIT_UNATTACHED")
	for key := range harnessBlockedEnvKeys {
		env = removeEnv(env, key)
	}
	env = upsertEnv(env, "HOME", home)
	env = upsertEnv(env, "AMUX_COLOR_PROFILE", "TrueColor")
	env = append(env, "AMUX_READY_FD=3", "AMUX_SHUTDOWN_FD=4", "AMUX_NO_WATCH=1", "AMUX_DISABLE_META_REFRESH=1")
	if exitUnattached {
		env = append(env, "AMUX_EXIT_UNATTACHED=1")
	}
	env = appendHarnessExtraEnv(env, extraEnv)

	// Write config to a temp file and pass via AMUX_CONFIG if provided.
	if configContent != "" {
		configDir := tb.TempDir()
		configPath := filepath.Join(configDir, "config.toml")
		if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
			tb.Fatalf("writing config: %v", err)
		}
		env = append(env, "AMUX_CONFIG="+configPath)
	}

	// Give each test its own GOCOVERDIR subdirectory. Without this, all
	// parallel amux processes (servers + short-lived CLI commands) race on
	// covmeta.* file renames in the shared directory, causing intermittent
	// "rename: no such file or directory" errors that corrupt CLI output.
	var coverDir string
	if gocoverDir != "" {
		coverDir = filepath.Join(gocoverDir, session)
		os.MkdirAll(coverDir, 0755)
		env = upsertEnv(env, "GOCOVERDIR", coverDir)
	}
	cmd.Env = env

	logDir := server.SocketDir()
	os.MkdirAll(logDir, 0700)
	logPath := filepath.Join(logDir, session+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		tb.Fatalf("opening log: %v", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		readPipe.Close()
		writePipe.Close()
		shutdownReadPipe.Close()
		shutdownWritePipe.Close()
		tb.Fatalf("starting server: %v", err)
	}
	writePipe.Close() // close write end in parent
	shutdownWritePipe.Close()
	logFile.Close()

	// Block until server signals readiness (after net.Listen succeeds).
	readPipe.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 64)
	n, err := readPipe.Read(buf)
	readPipe.Close()
	if err != nil || !strings.Contains(string(buf[:n]), "ready") {
		logData, _ := os.ReadFile(logPath)
		var waitErr error
		if err != nil && os.IsTimeout(err) {
			_ = cmd.Process.Kill()
			waitErr = cmd.Wait()
		} else {
			waitErr = cmd.Wait()
		}
		shutdownReadPipe.Close()
		tb.Fatalf("server ready signal not received: err=%v, buf=%q, pid=%d, waitErr=%v\nserver log:\n%s", err, string(buf[:n]), cmd.Process.Pid, waitErr, string(logData))
	}

	h := &ServerHarness{
		tb:               tb,
		session:          session,
		cmd:              cmd,
		home:             home,
		cols:             cols,
		rows:             rows,
		coverDir:         coverDir,
		extraEnv:         append([]string(nil), extraEnv...),
		logPath:          logPath,
		exitUnattached:   exitUnattached,
		ownsProcessGroup: true,
		shutdownPipe:     shutdownReadPipe,
	}
	h.startProcessWait()
	tb.Cleanup(h.cleanup)

	// Attach a headless client — seeds the first pane and stays connected
	// so capture requests route through client-side emulators.
	sockPath := server.SocketPath(session)
	client, err := newHeadlessClient(sockPath, session, cols, rows)
	if err != nil {
		cmd.Process.Kill()
		tb.Fatalf("attaching headless client: %v", err)
	}
	if err := client.waitCommandReady(); err != nil {
		client.close()
		cmd.Process.Kill()
		tb.Fatalf("headless client command-ready: %v", err)
	}
	h.client = client
	if keepalive {
		secondary, err := newHeadlessClient(sockPath, session, cols, rows)
		if err != nil {
			h.client.close()
			cmd.Process.Kill()
			tb.Fatalf("attaching keepalive headless client: %v", err)
		}
		h.keepalive = secondary
	}

	return h
}

func (h *ServerHarness) ensureControlClient() error {
	h.tb.Helper()
	if h.client != nil {
		return nil
	}

	client, err := newHeadlessClient(server.SocketPath(h.session), h.session, h.cols, h.rows)
	if err != nil {
		return err
	}
	if err := client.waitCommandReady(); err != nil {
		client.close()
		return err
	}
	h.client = client
	return nil
}

func (h *ServerHarness) signalServer(sig os.Signal) error {
	h.tb.Helper()
	if h == nil || h.cmd == nil || h.cmd.Process == nil {
		return fmt.Errorf("server process is not running")
	}
	pid := h.cmd.Process.Pid
	if !serverProcessMatchesSession(pid, h.session) {
		return fmt.Errorf("server process %d no longer matches session %s", pid, h.session)
	}
	return h.cmd.Process.Signal(sig)
}

// cleanup detaches the headless clients, sends SIGINT for graceful shutdown
// (coverage flush) when needed, then cleans up the socket and log files. As a
// fallback, kills the harness-owned process tree without touching later tests.
func (h *ServerHarness) cleanup() {
	if h.keepalive != nil {
		h.keepalive.close()
		h.keepalive = nil
	}
	if h.client != nil {
		h.client.close()
		h.client = nil
	}

	serverPid := 0
	if h.cmd != nil && h.cmd.Process != nil {
		serverPid = h.cmd.Process.Pid
	}
	if h.processWaitComplete() {
		h.cmd = nil
	}

	gracefulShutdown := h.shutdownPipe == nil
	switch {
	case h.cmd == nil || h.cmd.Process == nil:
	case h.exitUnattached:
		if h.shutdownPipe != nil {
			gracefulShutdown = h.waitForShutdownSignalWithin(5 * time.Second)
		}
	default:
		if serverProcessMatchesSession(serverPid, h.session) {
			_ = h.cmd.Process.Signal(os.Interrupt)
		}
		if h.shutdownPipe != nil {
			gracefulShutdown = h.waitForShutdownSignalWithin(5 * time.Second)
		}
	}

	if h.cmd != nil && !h.waitForProcessExit(3*time.Second) {
		if serverProcessMatchesSession(serverPid, h.session) {
			h.killServerProcessTree(serverPid)
		}
		if !h.waitForProcessExit(3 * time.Second) {
			h.tb.Fatalf("server process %d did not exit during harness cleanup", serverPid)
		}
	} else if h.cmd != nil && !gracefulShutdown {
		// The process exited before we observed the explicit shutdown signal.
		// Treat the cleanup as complete once the server is gone.
	}

	if h.shutdownPipe != nil {
		h.shutdownPipe.Close()
		h.shutdownPipe = nil
	}
	h.cmd = nil
	if h.tb != nil && h.tb.Failed() {
		if state := h.processStateSummary(); state != "" {
			h.tb.Logf("server process state: %s", state)
		}
		if tail := h.serverLogTail(diagnosticLogTailBytes); tail != "" {
			h.tb.Logf("server log tail:\n%s", tail)
		} else if h.logPath != "" {
			h.tb.Logf("server log unavailable at %s", h.logPath)
		}
		if h.home != "" {
			h.tb.Logf("harness home was cleaned by testing tempdir: %s", h.home)
		}
		return
	}
	socketDir := server.SocketDir()
	os.Remove(filepath.Join(socketDir, h.session))
	os.Remove(filepath.Join(socketDir, h.session+".log"))
	if h.home != "" {
		_ = os.RemoveAll(h.home)
		h.home = ""
	}
	h.logPath = ""
}

func (h *ServerHarness) runtimeState() string {
	h.tb.Helper()

	pid := 0
	if h.cmd != nil && h.cmd.Process != nil {
		pid = h.cmd.Process.Pid
	}
	alive := false
	if pid != 0 && syscall.Kill(pid, 0) == nil {
		alive = true
	}

	socketState := "missing"
	if _, err := os.Stat(server.SocketPath(h.session)); err == nil {
		socketState = "present"
	} else if !os.IsNotExist(err) {
		socketState = err.Error()
	}

	exitUnattached := "unknown"
	procState := "unknown"
	if pid != 0 && alive {
		if out, err := exec.Command("ps", "eww", "-p", strconv.Itoa(pid), "-o", "command=").Output(); err == nil {
			exitUnattached = fmt.Sprintf("%t", strings.Contains(string(out), "AMUX_EXIT_UNATTACHED=1"))
		}
		if out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output(); err == nil {
			procState = strings.TrimSpace(string(out))
		}
	}

	return fmt.Sprintf("pid=%d alive=%t state=%s socket=%s client_attached=%t exit_unattached=%s", pid, alive, procState, socketState, h.client != nil, exitUnattached)
}

// killChildrenByPid sends SIGKILL to all direct children of the given PID.
// Used as a fallback when process group kill doesn't reach all descendants.
func killChildrenByPid(pid int) {
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if childPID, err := strconv.Atoi(line); err == nil {
			syscall.Kill(childPID, syscall.SIGKILL)
		}
	}
}

func (h *ServerHarness) pushWaitState(state string) func() {
	h.diagMu.Lock()
	prev := h.currentWait
	h.currentWait = state
	h.diagMu.Unlock()
	return func() {
		h.diagMu.Lock()
		h.currentWait = prev
		h.diagMu.Unlock()
	}
}

func (h *ServerHarness) pushCommandState(state string) func() {
	h.diagMu.Lock()
	prev := h.currentCommand
	h.currentCommand = state
	h.diagMu.Unlock()
	return func() {
		h.diagMu.Lock()
		h.currentCommand = prev
		h.diagMu.Unlock()
	}
}

func (h *ServerHarness) diagnosticState() (wait, cmd string) {
	h.diagMu.Lock()
	defer h.diagMu.Unlock()
	return h.currentWait, h.currentCommand
}

func (h *ServerHarness) commandWithContext(ctx context.Context, args ...string) *exec.Cmd {
	cmdArgs := append([]string{"-s", h.session}, args...)
	cmd := exec.CommandContext(ctx, amuxBin, cmdArgs...)
	env := os.Environ()
	for key := range harnessBlockedEnvKeys {
		env = removeEnv(env, key)
	}
	env = upsertEnv(env, "HOME", h.home)
	if h.coverDir != "" {
		env = upsertEnv(env, "GOCOVERDIR", h.coverDir)
	}
	env = appendHarnessExtraEnv(env, h.extraEnv)
	cmd.Env = env
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.WaitDelay = harnessCommandWaitDelay
	cmd.Cancel = func() error {
		return killCmdProcessGroup(cmd)
	}
	return cmd
}

func appendHarnessExtraEnv(env, extraEnv []string) []string {
	for _, entry := range extraEnv {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			env = append(env, entry)
			continue
		}
		if _, blocked := harnessBlockedEnvKeys[key]; blocked {
			continue
		}
		env = upsertEnv(env, key, value)
	}
	return env
}

func formatHarnessCommandResult(cmdName string, msg *server.Message) string {
	if msg == nil {
		return ""
	}
	if msg.CmdErr != "" {
		return fmt.Sprintf("amux %s: %s\n", cmdName, msg.CmdErr)
	}
	return msg.CmdOutput
}

var attachedClientCommands = map[string]bool{
	"_layout-json": true,
	"cursor":       true,
	"focus":        true,
	"new-window":   true,
	"send-keys":    true,
	"split":        true,
	"status":       true,
	"wait":         true,
}

var attachedClientFallbackSafeCommands = map[string]bool{
	"_layout-json": true,
	"cursor":       true,
	"status":       true,
	"wait":         true,
}

func attachedClientAllowedAfterReload(args []string) bool {
	if len(args) == 0 {
		return false
	}
	return attachedClientFallbackSafeCommands[args[0]]
}

func (h *ServerHarness) attachedClientKnowsPane(ref string) bool {
	if h == nil || h.client == nil || ref == "" {
		return false
	}

	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(h.client.renderer.CaptureJSON(nil)), &capture); err != nil {
		return false
	}
	for _, pane := range capture.Panes {
		if pane.Name == ref || strings.HasPrefix(pane.Name, ref) || strconv.FormatUint(uint64(pane.ID), 10) == ref {
			return true
		}
	}
	return false
}

func (h *ServerHarness) canUseAttachedClient(args []string) bool {
	if len(args) == 0 {
		return false
	}
	cmdName := args[0]
	if !attachedClientCommands[cmdName] {
		return false
	}
	h.commandConnMu.Lock()
	postReloadProbes := h.postReloadProbes
	h.commandConnMu.Unlock()
	if postReloadProbes {
		switch {
		case attachedClientAllowedAfterReload(args):
		case cmdName == "send-keys" && len(args) >= 2 && h.attachedClientKnowsPane(args[1]):
		default:
			return false
		}
	}
	if h.client == nil || h.client.isClosing() {
		return false
	}
	select {
	case <-h.client.done:
		return false
	default:
		return true
	}
}

func (h *ServerHarness) waitForAttachedClientReady(timeout time.Duration) bool {
	hc := h.client
	if hc == nil || hc.isClosing() {
		return false
	}

	waitFor := timeout
	if _, awaitingReconnect := h.attachedClientCommandState(); !awaitingReconnect && waitFor > 2*time.Second {
		waitFor = 2 * time.Second
	}
	deadline := time.Now().Add(waitFor)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	for {
		conn := hc.currentConn()
		lastConn, awaitingReconnect := h.attachedClientCommandState()
		switch {
		case conn == nil:
		case !awaitingReconnect && conn == lastConn:
			return true
		case awaitingReconnect && conn == lastConn:
			// reload-server intentionally tears down the old connection; do not
			// treat the pre-reload socket as ready once we start waiting for the
			// replacement client to reconnect.
		default:
			readyWait := time.Until(deadline)
			if readyWait <= 0 {
				return false
			}
			readyCh := make(chan error, 1)
			go func() {
				readyCh <- hc.waitCommandReady()
			}()

			select {
			case err := <-readyCh:
				if err != nil {
					return false
				}
				h.commandConnMu.Lock()
				h.lastCommandConn = hc.currentConn()
				h.awaitingReconnect = false
				h.commandConnMu.Unlock()
				return h.lastCommandConn != nil
			case <-time.After(readyWait):
				return false
			case <-hc.closing:
				return false
			case <-hc.done:
				return false
			}
		}
		if !time.Now().Before(deadline) {
			return false
		}
		select {
		case <-hc.closing:
			return false
		case <-hc.done:
			return false
		case <-ticker.C:
		}
	}
}

func (h *ServerHarness) attachedClientCommandState() (net.Conn, bool) {
	h.commandConnMu.Lock()
	defer h.commandConnMu.Unlock()
	return h.lastCommandConn, h.awaitingReconnect
}

func (h *ServerHarness) runAttachedClientCommand(timeout time.Duration, args ...string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("no command provided")
	}
	hc := h.client
	if hc == nil {
		return "", fmt.Errorf("headless client not connected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req := headlessCommand{
		msg: &server.Message{
			Type:    server.MsgTypeCommand,
			CmdName: args[0],
			CmdArgs: args[1:],
		},
		reply: make(chan *server.Message, 1),
	}

	select {
	case hc.cmdReqs <- req:
	case <-ctx.Done():
		return "", fmt.Errorf("timed out after %v", timeout)
	case <-hc.closing:
		return "", fmt.Errorf("headless client closed")
	case <-hc.done:
		return "", fmt.Errorf("headless client closed")
	}

	select {
	case msg := <-req.reply:
		return formatHarnessCommandResult(args[0], msg), nil
	case <-ctx.Done():
		return "", fmt.Errorf("timed out after %v", timeout)
	case <-hc.closing:
		select {
		case msg := <-req.reply:
			return formatHarnessCommandResult(args[0], msg), nil
		default:
			return "", fmt.Errorf("headless client closed")
		}
	case <-hc.done:
		select {
		case msg := <-req.reply:
			return formatHarnessCommandResult(args[0], msg), nil
		default:
			return "", fmt.Errorf("headless client closed")
		}
	}
}

func (h *ServerHarness) runCmdWithTimeout(timeout time.Duration, track bool, args ...string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("no command provided")
	}
	args = rewriteLegacyHarnessArgs(args)
	if track {
		restore := h.pushCommandState("amux " + strings.Join(args, " "))
		defer restore()
	}

	// The reload command itself must use a short-lived CLI subprocess because
	// it intentionally tears down the attached client connection mid-command.
	// Subsequent read-only probes can reuse the reconnected headless client.
	// Mutating commands keep the CLI subprocess path after reload because
	// takeover flows depend on the extra handshake time that path preserves.
	forceSubprocess := args[0] == "reload-server"
	if forceSubprocess {
		h.commandConnMu.Lock()
		if h.client != nil {
			h.lastCommandConn = h.client.currentConn()
		} else {
			h.lastCommandConn = nil
		}
		h.awaitingReconnect = true
		h.postReloadProbes = true
		h.commandConnMu.Unlock()
	}
	if !forceSubprocess {
		_, awaitingReconnect := h.attachedClientCommandState()
		forceSubprocess = awaitingReconnect && attachedClientFallbackSafeCommands[args[0]]
	}

	if !forceSubprocess && h.canUseAttachedClient(args) && h.waitForAttachedClientReady(timeout) {
		out, err := h.runAttachedClientCommand(timeout, args...)
		if err == nil || !attachedClientFallbackSafeCommands[args[0]] {
			return out, err
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := h.commandWithContext(ctx, args...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("timed out after %v", timeout)
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return string(out), err
	}
	return string(out), nil
}

func (h *ServerHarness) diagnosticProbe(args ...string) (string, error) {
	return h.runCmdWithTimeout(diagnosticProbeTimeout, false, args...)
}

func (h *ServerHarness) diagnosticSnapshot(reason string) string {
	wait, cmd := h.diagnosticState()
	data := diagnosticSnapshotData{
		TestName: h.tb.Name(),
		Session:  h.session,
		Wait:     wait,
		Command:  cmd,
		Process:  h.processStateSummary(),
	}

	if out, err := h.diagnosticProbe("cursor", "layout"); err != nil {
		data.Generation = fmt.Sprintf("cursor layout probe: %v", err)
		if trimmed := strings.TrimSpace(out); trimmed != "" {
			data.Generation += "\ncursor layout output: " + truncateDiagnostic(trimmed, diagnosticOutputLimit)
		}
	} else {
		data.Generation = "cursor layout: " + strings.TrimSpace(out)
	}

	if out, err := h.diagnosticProbe("capture", "--format", "json"); err != nil {
		data.JSONCaptureSummary = fmt.Sprintf("json capture probe: %v", err)
		if trimmed := strings.TrimSpace(out); trimmed != "" {
			data.JSONCaptureSummary += "\njson capture output:\n" + truncateDiagnostic(trimmed, diagnosticOutputLimit)
		}
	} else {
		data.JSONCaptureSummary = summarizeDiagnosticCaptureJSON(out)
	}

	if out, err := h.diagnosticProbe("capture"); err != nil {
		data.PlainCapture = fmt.Sprintf("plain capture probe: %v", err)
		if trimmed := strings.TrimSpace(out); trimmed != "" {
			data.PlainCapture += "\nplain capture output:\n" + truncateDiagnostic(trimmed, diagnosticOutputLimit)
		}
	} else {
		data.PlainCapture = truncateDiagnostic(out, diagnosticOutputLimit)
	}

	data.ServerLogTail = h.serverLogTail(diagnosticLogTailBytes)
	return formatDiagnosticSnapshot(reason, data)
}

type diagnosticSnapshotData struct {
	TestName           string
	Session            string
	Wait               string
	Command            string
	Process            string
	Generation         string
	JSONCaptureSummary string
	PlainCapture       string
	ServerLogTail      string
}

func formatDiagnosticSnapshot(reason string, data diagnosticSnapshotData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- amux harness diagnostics: %s ---\n", reason)
	fmt.Fprintf(&b, "test: %s\nsession: %s\n", data.TestName, data.Session)
	if data.Wait != "" {
		fmt.Fprintf(&b, "wait: %s\n", data.Wait)
	}
	if data.Command != "" {
		fmt.Fprintf(&b, "command: %s\n", data.Command)
	}
	if data.Process != "" {
		fmt.Fprintf(&b, "%s\n", data.Process)
	}
	if data.Generation != "" {
		fmt.Fprintf(&b, "%s\n", data.Generation)
	}
	if data.JSONCaptureSummary != "" {
		fmt.Fprintf(&b, "\njson capture summary:\n%s\n", data.JSONCaptureSummary)
	}
	if data.PlainCapture != "" {
		fmt.Fprintf(&b, "\nplain capture:\n%s\n", data.PlainCapture)
	}
	if data.ServerLogTail != "" {
		fmt.Fprintf(&b, "\nserver log tail:\n%s\n", data.ServerLogTail)
	}
	return b.String()
}

func summarizeDiagnosticCaptureJSON(raw string) string {
	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(raw), &capture); err != nil {
		return fmt.Sprintf("unable to parse json capture: %v\nraw:\n%s", err, truncateDiagnostic(raw, diagnosticOutputLimit))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "window=%d:%s index=%d size=%dx%d panes=%d notice=%q",
		capture.Window.ID, capture.Window.Name, capture.Window.Index, capture.Width, capture.Height, len(capture.Panes), capture.Notice)
	for _, pane := range capture.Panes {
		pos := "unknown"
		if pane.Position != nil {
			pos = fmt.Sprintf("%d,%d %dx%d", pane.Position.X, pane.Position.Y, pane.Position.Width, pane.Position.Height)
		}
		var flags []string
		if pane.Active {
			flags = append(flags, "active")
		}
		if pane.Zoomed {
			flags = append(flags, "zoomed")
		}
		if pane.CopyMode {
			flags = append(flags, "copy")
		}
		flagText := ""
		if len(flags) > 0 {
			flagText = " flags=" + strings.Join(flags, ",")
		}
		firstLine := ""
		if len(pane.Content) > 0 {
			firstLine = truncateDiagnostic(strings.TrimRight(pane.Content[0], " "), 120)
		}
		fmt.Fprintf(&b, "\n- %s id=%d pos=%s cursor=%d,%d idle=%t cmd=%q%s",
			pane.Name, pane.ID, pos, pane.Cursor.Col, pane.Cursor.Row, pane.Idle, pane.CurrentCommand, flagText)
		if pane.ConnStatus != "" {
			fmt.Fprintf(&b, " conn=%s", pane.ConnStatus)
		}
		if firstLine != "" {
			fmt.Fprintf(&b, " first=%q", firstLine)
		}
		if pane.Error != nil {
			fmt.Fprintf(&b, " error=%s:%s", pane.Error.Code, pane.Error.Message)
		}
	}
	return b.String()
}

func (h *ServerHarness) serverLogTail(maxBytes int) string {
	if h.logPath == "" {
		return ""
	}
	data, err := os.ReadFile(h.logPath)
	if err != nil || len(data) == 0 {
		return ""
	}
	return tailDiagnostic(string(data), maxBytes)
}

func (h *ServerHarness) serverWaitStatus() string {
	if h.waitDone == nil {
		return ""
	}
	select {
	case <-h.waitDone:
		if h.waitErr == nil {
			return "server process exited cleanly"
		}
		return "server process exited: " + h.waitErr.Error()
	default:
		return "server process still running"
	}
}

func truncateDiagnostic(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "\n...[truncated]..."
}

func tailDiagnostic(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	s = s[len(s)-max:]
	if idx := strings.IndexByte(s, '\n'); idx >= 0 && idx+1 < len(s) {
		s = s[idx+1:]
	}
	return "...[truncated]...\n" + s
}

func (h *ServerHarness) killServerProcessTree(serverPid int) {
	if serverPid == 0 {
		return
	}
	if h.ownsProcessGroup {
		_ = syscall.Kill(-serverPid, syscall.SIGKILL)
		return
	}
	killChildrenByPid(serverPid)
	_ = syscall.Kill(serverPid, syscall.SIGKILL)
}

func (h *ServerHarness) startProcessWait() {
	if h == nil || h.cmd == nil {
		return
	}
	h.waitOnce.Do(func() {
		h.waitDone = make(chan struct{})
		cmd := h.cmd
		go func() {
			err := cmd.Wait()
			h.waitMu.Lock()
			h.waitErr = err
			h.waitMu.Unlock()
			close(h.waitDone)
		}()
	})
}

func (h *ServerHarness) processWaitComplete() bool {
	if h == nil || h.waitDone == nil {
		return false
	}
	select {
	case <-h.waitDone:
		return true
	default:
		return false
	}
}

func (h *ServerHarness) processStateSummary() string {
	if h == nil {
		return ""
	}
	if h.waitDone != nil {
		select {
		case <-h.waitDone:
			h.waitMu.Lock()
			err := h.waitErr
			h.waitMu.Unlock()
			if err != nil {
				return fmt.Sprintf("server exit: %v", err)
			}
			return "server exit: clean"
		default:
		}
	}
	if h.cmd != nil && h.cmd.Process != nil {
		pid := h.cmd.Process.Pid
		if serverProcessMatchesSession(pid, h.session) {
			return fmt.Sprintf("server pid: %d (running)", pid)
		}
		return fmt.Sprintf("server pid: %d (session no longer matches)", pid)
	}
	return ""
}

// ---------------------------------------------------------------------------
// CLI command helpers — all synchronous, zero polling
// ---------------------------------------------------------------------------

// runCmdTimeout is the default per-command timeout. It must be longer than any
// server-side --timeout flag (wait-idle uses 20s) but short enough that a stuck
// command doesn't consume the entire test binary timeout (300s in CI).
const runCmdTimeout = 30 * time.Second

const (
	diagnosticProbeTimeout = 1500 * time.Millisecond
	diagnosticLogTailBytes = 8 << 10
	diagnosticOutputLimit  = 12 << 10
)

// runCmd executes an amux command targeting this test's session. When the
// harness still has its attached headless client, the command goes through
// that persistent control channel; otherwise it falls back to a short-lived
// CLI subprocess. The command is bounded by runCmdTimeout either way.
func (h *ServerHarness) runCmd(args ...string) string {
	h.tb.Helper()
	out, err := h.runCmdWithTimeout(runCmdTimeout, true, args...)
	if err != nil {
		h.tb.Fatalf("runCmd failed for amux %s: %v\noutput so far:\n%s\n%s",
			strings.Join(args, " "), err, out, h.diagnosticSnapshot("runCmd failure"))
	}
	return out
}

func (h *ServerHarness) runControlCmd(args ...string) string {
	h.tb.Helper()
	if len(args) == 0 {
		h.tb.Fatal("runControlCmd requires a command")
	}
	if err := h.ensureControlClient(); err != nil {
		return h.runCmd(args...)
	}

	restore := h.pushCommandState("control " + strings.Join(args, " "))
	defer restore()

	msg := h.client.runCommand(args[0], args[1:]...)
	switch msg.CmdErr {
	case "headless client closed", "headless client not connected", "timeout waiting for command result":
		h.client.close()
		h.client = nil
		if err := h.ensureControlClient(); err == nil {
			msg = h.client.runCommand(args[0], args[1:]...)
		} else {
			return h.runCmd(args...)
		}
	}
	if msg.CmdErr != "" {
		return fmt.Sprintf("amux %s: %s", args[0], msg.CmdErr)
	}
	return msg.CmdOutput
}

func (h *ServerHarness) waitForShutdownSignal(timeout time.Duration) {
	h.tb.Helper()
	if !h.waitForShutdownSignalWithin(timeout) {
		h.tb.Fatal("server shutdown signal not received")
	}
}

func (h *ServerHarness) waitForShutdownSignalWithin(timeout time.Duration) bool {
	h.tb.Helper()
	if h.shutdownPipe == nil {
		return false
	}
	_ = h.shutdownPipe.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 64)
	n, err := h.shutdownPipe.Read(buf)
	h.shutdownPipe.Close()
	h.shutdownPipe = nil
	return err == nil && strings.Contains(string(buf[:n]), "shutdown")
}

func (h *ServerHarness) waitForProcessExit(timeout time.Duration) bool {
	h.tb.Helper()
	if h.cmd == nil {
		return true
	}
	h.startProcessWait()
	if h.processWaitComplete() {
		h.cmd = nil
		return true
	}
	select {
	case <-h.waitDone:
		h.cmd = nil
		return true
	case <-time.After(timeout):
		return false
	}
}

// capture returns the server-side composited screen (plain text 2D grid).
func (h *ServerHarness) capture() string {
	h.tb.Helper()
	return h.runCmd("capture")
}

// captureLines returns the capture output split into rows.
func (h *ServerHarness) captureLines() []string {
	h.tb.Helper()
	return strings.Split(h.capture(), "\n")
}

// captureContentLines returns capture lines excluding the global bar.
func (h *ServerHarness) captureContentLines() []string {
	h.tb.Helper()
	var out []string
	for _, line := range h.captureLines() {
		if !isGlobalBar(line) {
			out = append(out, line)
		}
	}
	return out
}

// captureVerticalBorderCol finds a consistent vertical border column.
func (h *ServerHarness) captureVerticalBorderCol() int {
	h.tb.Helper()
	return findVerticalBorderCol(h.captureContentLines())
}

// assertScreen fails the test if fn returns false for the current screen.
func (h *ServerHarness) assertScreen(msg string, fn func(string) bool) {
	h.tb.Helper()
	screen := h.capture()
	if !fn(screen) {
		h.tb.Errorf("%s\nScreen:\n%s", msg, screen)
	}
}

// captureJSON returns the full-screen JSON capture as a parsed struct.
func (h *ServerHarness) captureJSON() proto.CaptureJSON {
	h.tb.Helper()
	out := h.runCmd("capture", "--format", "json")
	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		h.tb.Fatalf("captureJSON: %v\nraw: %s\n%s", err, out, h.diagnosticSnapshot("captureJSON failure"))
	}
	return capture
}

// jsonPane finds a pane by name in a CaptureJSON, or fails the test.
// Also fails if Position is nil (full-screen captures always set it).
func (h *ServerHarness) jsonPane(capture proto.CaptureJSON, name string) proto.CapturePane {
	h.tb.Helper()
	return jsonPaneFor(h.tb, capture, name)
}

// assertActive asserts that the named pane is the active pane.
func (h *ServerHarness) assertActive(name string) {
	h.tb.Helper()
	c := h.captureJSON()
	p := h.jsonPane(c, name)
	if !p.Active {
		h.tb.Errorf("%s should be active, but is not", name)
	}
}

// assertInactive asserts that the named pane is not the active pane.
func (h *ServerHarness) assertInactive(name string) {
	h.tb.Helper()
	c := h.captureJSON()
	p := h.jsonPane(c, name)
	if p.Active {
		h.tb.Errorf("%s should be inactive, but is active", name)
	}
}

// globalBar returns the global bar line from the capture.
func (h *ServerHarness) globalBar() string {
	h.tb.Helper()
	return globalBarFromLines(h.captureLines())
}

// ---------------------------------------------------------------------------
// Synchronization primitives — zero polling replacements
// ---------------------------------------------------------------------------

// waitFor blocks until substr appears in the named pane's screen content.
// Uses the server's wait-for command (blocking, zero polling).
func (h *ServerHarness) waitFor(pane, substr string) {
	h.tb.Helper()
	h.waitForTimeout(pane, substr, "10s")
}

// waitForTimeout is like waitFor but with a custom timeout.
func (h *ServerHarness) waitForTimeout(pane, substr, timeout string) {
	h.tb.Helper()
	restore := h.pushWaitState(fmt.Sprintf("waiting for %s to contain %q (timeout %s)", pane, substr, timeout))
	defer restore()
	out := h.runCmd("wait", "content", pane, substr, "--timeout", timeout)
	if strings.Contains(out, "timeout") || strings.Contains(out, "not found") {
		h.tb.Fatalf("wait-for %q in %s: %s\n%s", substr, pane, strings.TrimSpace(out), h.diagnosticSnapshot("wait-for failure"))
	}
}

// waitBusy blocks until the named pane has child processes (a command is running).
// Uses the server's process-based wait-busy command.
func (h *ServerHarness) waitBusy(pane string) {
	h.tb.Helper()
	restore := h.pushWaitState(fmt.Sprintf("waiting for %s to become busy", pane))
	defer restore()
	out := h.runCmd("wait", "busy", pane, "--timeout", "15s")
	if strings.Contains(out, "timeout") || strings.Contains(out, "not found") {
		h.tb.Fatalf("wait-busy %s: %s\n%s", pane, strings.TrimSpace(out), h.diagnosticSnapshot("wait-busy failure"))
	}
}

// startLongSleep starts a long-running command in the named pane and waits
// until the server reports a child process for that pane.
func (h *ServerHarness) startLongSleep(pane string) {
	h.tb.Helper()
	h.sendKeys(pane, "sleep 300", "Enter")
	h.waitBusy(pane)
}

// waitIdle blocks until the named pane has emitted an idle transition and no
// foreground child process is still running.
func (h *ServerHarness) waitIdle(pane string) {
	h.tb.Helper()
	restore := h.pushWaitState(fmt.Sprintf("waiting for %s to become idle", pane))
	defer restore()
	out := h.runCmd("wait", "idle", pane, "--timeout", "20s")
	if strings.Contains(out, "timeout") || strings.Contains(out, "not found") {
		h.tb.Fatalf("wait-idle %s: %s\n%s", pane, strings.TrimSpace(out), h.diagnosticSnapshot("wait-idle failure"))
	}
}

// generation returns the current layout generation counter.
func (h *ServerHarness) generation() uint64 {
	h.tb.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var out string
	for {
		out = strings.TrimSpace(h.runCmd("cursor", "layout"))
		n, err := strconv.ParseUint(out, 10, 64)
		if err == nil {
			return n
		}
		if !isCommandConnectError(out) || !time.Now().Before(deadline) {
			h.tb.Fatalf("parsing generation: %v (output: %q)\n%s", err, out, h.diagnosticSnapshot("generation parse failure"))
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// waitLayout blocks until the layout generation exceeds afterGen.
func (h *ServerHarness) waitLayout(afterGen uint64) {
	h.tb.Helper()
	h.waitLayoutTimeout(afterGen, "5s")
}

// waitLayoutTimeout is like waitLayout but with a custom timeout.
func (h *ServerHarness) waitLayoutTimeout(afterGen uint64, timeout string) {
	h.tb.Helper()
	restore := h.pushWaitState(fmt.Sprintf("waiting for layout generation > %d (timeout %s)", afterGen, timeout))
	defer restore()
	out := h.runCmd("wait", "layout", "--after", strconv.FormatUint(afterGen, 10), "--timeout", timeout)
	if strings.Contains(out, "timeout") {
		h.tb.Fatalf("wait-layout timed out after generation %d\n%s", afterGen, h.diagnosticSnapshot("wait-layout failure"))
	}
}

// waitLayoutOrTimeout is like waitLayoutTimeout but returns false on timeout
// instead of failing the test. Used in polling loops where timeout is a valid
// exit condition rather than a test failure.
func (h *ServerHarness) waitLayoutOrTimeout(afterGen uint64, timeout string) bool {
	h.tb.Helper()
	out := h.runCmd("wait", "layout", "--after", strconv.FormatUint(afterGen, 10), "--timeout", timeout)
	return !strings.Contains(out, "timeout") && !isCommandConnectError(out)
}

// waitForFunc polls the compositor capture until fn returns true or timeout.
// It waits on layout generation bumps between capture checks, avoiding sleep-based polling.
func (h *ServerHarness) waitForFunc(fn func(string) bool, timeout time.Duration) bool {
	h.tb.Helper()
	restore := h.pushWaitState(fmt.Sprintf("waiting for plain capture predicate (timeout %v)", timeout))
	defer restore()
	deadline := time.Now().Add(timeout)
	gen := h.generation()
	for time.Now().Before(deadline) {
		if fn(h.capture()) {
			return true
		}
		waitFor := time.Until(deadline)
		if waitFor > 250*time.Millisecond {
			waitFor = 250 * time.Millisecond
		}
		if !h.waitLayoutOrTimeout(gen, waitFor.String()) {
			return fn(h.capture())
		}
		gen = h.generation()
	}
	return false
}

// waitForCaptureJSON polls JSON capture until fn returns true or timeout.
// It waits on layout generation bumps between capture checks instead of sleeping.
func (h *ServerHarness) waitForCaptureJSON(fn func(proto.CaptureJSON) bool, timeout time.Duration) bool {
	h.tb.Helper()
	restore := h.pushWaitState(fmt.Sprintf("waiting for json capture predicate (timeout %v)", timeout))
	defer restore()
	_, ok := waitForCaptureJSONWithLayout(
		h.captureJSON,
		h.generation,
		func(afterGen uint64, waitFor time.Duration) bool {
			return h.waitLayoutOrTimeout(afterGen, waitFor.String())
		},
		fn,
		timeout,
	)
	return ok
}

// waitForPaneContent polls the client-rendered pane capture until substr
// appears in the named pane's content or timeout elapses.
func (h *ServerHarness) waitForPaneContent(pane, substr string, timeout time.Duration) {
	h.tb.Helper()
	restore := h.pushWaitState(fmt.Sprintf("waiting for %s pane content to contain %q (timeout %v)", pane, substr, timeout))
	defer restore()

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		c := h.captureJSON()
		for _, p := range c.Panes {
			if p.Name != pane {
				continue
			}
			if strings.Contains(strings.Join(p.Content, "\n"), substr) {
				return
			}
			break
		}
		<-ticker.C
	}

	h.tb.Fatalf("pane %s content did not contain %q within %v\n%s", pane, substr, timeout, h.diagnosticSnapshot("waitForPaneContent failure"))
}

// ---------------------------------------------------------------------------
// Split helpers — synchronous via CLI, no keybinding simulation
// ---------------------------------------------------------------------------

// activePaneName returns the name of the currently active pane via JSON capture.
func (h *ServerHarness) activePaneName() string {
	h.tb.Helper()
	c := h.captureJSON()
	for _, p := range c.Panes {
		if p.Active {
			return p.Name
		}
	}
	h.tb.Fatal("no active pane found in capture")
	return ""
}

// doSplit is a layout-construction helper for tests. It clears the default
// single-pane pending lead so generic layout tests keep exercising ordinary
// split semantics, then runs the public split CLI command against the active
// pane, waits for the resulting layout update, and explicitly focuses the
// newly created pane so repeated calls keep building from the latest leaf.
// Tests that need raw split semantics should call runCmd("split", ...) directly.
func (h *ServerHarness) doSplit(args ...string) {
	h.tb.Helper()
	restore := h.pushWaitState(fmt.Sprintf("waiting for split %v to create a new pane", args))
	defer restore()
	before := h.layoutSnapshot()
	if before.Root.IsLeaf && len(before.Panes) == 1 && before.LeadPaneID == before.ActivePaneID {
		h.unsetLead()
		before.LeadPaneID = 0
	}
	gen := h.generation()
	pane, ok := activePaneNameFromLayout(before)
	if !ok {
		h.tb.Fatal("no active pane found in layout snapshot")
	}
	cmdArgs := append([]string{"split", pane}, args...)
	out := h.runCmd(cmdArgs...)
	if strings.Contains(out, "error") || strings.Contains(out, "cannot") {
		h.tb.Fatalf("split %v failed: %s", args, out)
	}
	h.waitLayout(gen)
	after := h.layoutSnapshot()
	if createdID, ok := splitCreatedPaneIDFromLayout(before, after); ok {
		h.doFocus(strconv.FormatUint(uint64(createdID), 10))
		return
	}
	h.tb.Fatalf("split %v created no detectable new pane; output: %s\nbefore panes: %+v\nafter panes: %+v", args, out, layoutPanes(before), layoutPanes(after))
}

// doFocus runs a focus command and waits for the layout generation to bump
// (ensuring the headless client has received the broadcast).
func (h *ServerHarness) doFocus(args ...string) string {
	h.tb.Helper()
	restore := h.pushWaitState(fmt.Sprintf("waiting for focus %v to update layout", args))
	defer restore()
	gen := h.generation()
	cmdArgs := append([]string{"focus"}, args...)
	out := h.runCmd(cmdArgs...)
	h.waitLayout(gen)
	return out
}

func (h *ServerHarness) unsetLead() {
	h.tb.Helper()
	out := h.runCmd("unset-lead")
	if strings.Contains(out, "error") || strings.Contains(out, "cannot") {
		h.tb.Fatalf("unset-lead failed: %s", out)
	}
}

// doSplitPane is like doSplit but takes an explicit pane name instead of
// querying the active pane. Use this when the capture may be stale
// (for example after reload).
func (h *ServerHarness) doSplitPane(pane string, args ...string) {
	h.tb.Helper()
	restore := h.pushWaitState(fmt.Sprintf("waiting for split %s %v to create a new pane", pane, args))
	defer restore()
	before := h.layoutSnapshot()
	if before.Root.IsLeaf && len(before.Panes) == 1 && before.LeadPaneID == before.ActivePaneID {
		h.unsetLead()
		before.LeadPaneID = 0
	}
	gen := h.generation()
	cmdArgs := append([]string{"split", pane}, args...)
	out := h.runCmd(cmdArgs...)
	if strings.Contains(out, "error") || strings.Contains(out, "cannot") {
		h.tb.Fatalf("split %s %v failed: %s", pane, args, out)
	}
	h.waitLayout(gen)
	after := h.layoutSnapshot()
	if createdID, ok := splitCreatedPaneIDFromLayout(before, after); ok {
		h.doFocus(strconv.FormatUint(uint64(createdID), 10))
		return
	}
	h.tb.Fatalf("split %s %v created no detectable new pane; output: %s\nbefore panes: %+v\nafter panes: %+v", pane, args, out, layoutPanes(before), layoutPanes(after))
}

func (h *ServerHarness) layoutSnapshot() *proto.LayoutSnapshot {
	h.tb.Helper()
	out := h.runCmd("_layout-json")
	var layout proto.LayoutSnapshot
	if err := json.Unmarshal([]byte(out), &layout); err != nil {
		h.tb.Fatalf("layoutSnapshot: %v\nraw: %s\n%s", err, out, h.diagnosticSnapshot("layout snapshot parse failure"))
	}
	return &layout
}

func activePaneNameFromLayout(layout *proto.LayoutSnapshot) (string, bool) {
	if layout == nil {
		return "", false
	}
	if len(layout.Windows) > 0 {
		for _, ws := range layout.Windows {
			if ws.ID != layout.ActiveWindowID {
				continue
			}
			for _, p := range ws.Panes {
				if p.ID == ws.ActivePaneID {
					return p.Name, true
				}
			}
			return "", false
		}
	}
	for _, p := range layout.Panes {
		if p.ID == layout.ActivePaneID {
			return p.Name, true
		}
	}
	return "", false
}

func layoutPanes(layout *proto.LayoutSnapshot) []proto.PaneSnapshot {
	if layout == nil {
		return nil
	}
	if len(layout.Windows) == 0 {
		return layout.Panes
	}
	panes := make([]proto.PaneSnapshot, 0)
	for _, ws := range layout.Windows {
		panes = append(panes, ws.Panes...)
	}
	return panes
}

func splitCreatedPaneIDFromLayout(before, after *proto.LayoutSnapshot) (uint32, bool) {
	beforeIDs := make(map[uint32]struct{}, len(layoutPanes(before)))
	for _, p := range layoutPanes(before) {
		beforeIDs[p.ID] = struct{}{}
	}
	var createdID uint32
	createdCount := 0
	for _, p := range layoutPanes(after) {
		if _, ok := beforeIDs[p.ID]; ok {
			continue
		}
		createdID = p.ID
		createdCount++
	}
	if createdCount != 1 {
		return 0, false
	}
	return createdID, true
}

func TestSplitCreatedPaneIDFromLayout(t *testing.T) {
	t.Parallel()

	before := &proto.LayoutSnapshot{
		Panes: []proto.PaneSnapshot{
			{ID: 1, Name: "pane-1"},
			{ID: 2, Name: "pane-2"},
		},
	}
	after := &proto.LayoutSnapshot{
		Panes: []proto.PaneSnapshot{
			{ID: 1, Name: "pane-1"},
			{ID: 2, Name: "pane-2"},
			{ID: 7, Name: "pane-7"},
		},
	}

	if got, ok := splitCreatedPaneIDFromLayout(before, after); !ok || got != 7 {
		t.Fatalf("splitCreatedPaneIDFromLayout() = (%d, %t), want (7, true)", got, ok)
	}
	if got, ok := splitCreatedPaneIDFromLayout(before, before); ok || got != 0 {
		t.Fatalf("splitCreatedPaneIDFromLayout() without new pane = (%d, %t), want (0, false)", got, ok)
	}
}

func TestFormatDiagnosticSnapshotIncludesWaitState(t *testing.T) {
	t.Parallel()

	snapshot := formatDiagnosticSnapshot("unit test", diagnosticSnapshotData{
		TestName:           "TestFormatDiagnosticSnapshotIncludesWaitState",
		Session:            "t-test",
		Wait:               `waiting for pane-1 to contain "$"`,
		Command:            `amux wait-for pane-1 "$" --timeout 10s`,
		Generation:         "generation: 42",
		JSONCaptureSummary: "window=1:main index=1 size=80x24 panes=1 notice=\"\"",
		PlainCapture:       "[pane-1]\n$",
		ServerLogTail:      "ready\n",
	})

	for _, want := range []string{
		"--- amux harness diagnostics: unit test ---",
		"test: TestFormatDiagnosticSnapshotIncludesWaitState",
		"session: t-test",
		`wait: waiting for pane-1 to contain "$"`,
		`command: amux wait-for pane-1 "$" --timeout 10s`,
		"generation: 42",
		"json capture summary:",
		"plain capture:",
		"server log tail:",
	} {
		if !strings.Contains(snapshot, want) {
			t.Fatalf("diagnostic snapshot missing %q\nsnapshot:\n%s", want, snapshot)
		}
	}
}

func TestSummarizeDiagnosticCaptureJSONIncludesPaneState(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(proto.CaptureJSON{
		Session: "t-test",
		Window:  proto.CaptureWindow{ID: 1, Name: "main", Index: 1},
		Width:   80,
		Height:  24,
		Panes: []proto.CapturePane{
			{
				ID:             1,
				Name:           "pane-1",
				Active:         true,
				Position:       &proto.CapturePos{X: 0, Y: 0, Width: 80, Height: 23},
				Cursor:         proto.CaptureCursor{Col: 7, Row: 0},
				Content:        []string{"PROMPT$"},
				Idle:           true,
				CurrentCommand: "bash",
			},
			{
				ID:             2,
				Name:           "pane-2",
				Position:       &proto.CapturePos{X: 0, Y: 0, Width: 40, Height: 10},
				Cursor:         proto.CaptureCursor{Col: 0, Row: 0},
				Content:        []string{"REMOTE"},
				ConnStatus:     "reconnecting",
				CurrentCommand: "ssh",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal capture json: %v", err)
	}

	summary := summarizeDiagnosticCaptureJSON(string(raw))
	for _, want := range []string{
		`window=1:main index=1 size=80x24 panes=2 notice=""`,
		`- pane-1 id=1 pos=0,0 80x23 cursor=7,0 idle=true cmd="bash" flags=active first="PROMPT$"`,
		`- pane-2 id=2 pos=0,0 40x10 cursor=0,0 idle=false cmd="ssh" conn=reconnecting first="REMOTE"`,
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("capture summary missing %q\nsummary:\n%s", want, summary)
		}
	}
}

func TestCommandWithContextSetsProcessGroupKillAndWaitDelay(t *testing.T) {
	t.Parallel()

	h := &ServerHarness{
		session: "t-config",
		home:    t.TempDir(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	cmd := h.commandWithContext(ctx, "capture", "--format", "json")
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("commandWithContext should run CLI subprocesses in their own process group")
	}
	if cmd.WaitDelay <= 0 {
		t.Fatal("commandWithContext should set WaitDelay so timed-out subprocesses cannot wedge Wait")
	}
	if cmd.Cancel == nil {
		t.Fatal("commandWithContext should override Cancel to kill the subprocess process group")
	}
}

func (h *ServerHarness) splitV()     { h.tb.Helper(); h.doSplit("v") }
func (h *ServerHarness) splitH()     { h.tb.Helper(); h.doSplit() }
func (h *ServerHarness) splitRootV() { h.tb.Helper(); h.doSplit("root", "v") }
func (h *ServerHarness) splitRootH() { h.tb.Helper(); h.doSplit("root") }

// ---------------------------------------------------------------------------
// Pane interaction — via CLI send-keys, no tmux
// ---------------------------------------------------------------------------

// sendKeys sends keystrokes to a specific pane via the server's send-keys command.
func (h *ServerHarness) sendKeys(pane string, keys ...string) {
	h.tb.Helper()
	args := append([]string{"send-keys", pane}, keys...)
	out := h.runCmd(args...)
	if strings.Contains(out, "not found") {
		h.tb.Fatalf("sendKeys to %s: %s", pane, strings.TrimSpace(out))
	}
}

func (h *ServerHarness) sendClientKeys(keys ...string) string {
	h.tb.Helper()
	pane := h.activePaneName()
	args := append([]string{"send-keys", pane, "--via", "client"}, keys...)
	return h.runCmd(args...)
}

func TestNewServerHarnessReturnsCommandReady(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	_ = h.generation()

	msg := h.attachAt(80, 24)
	if msg.Layout == nil {
		t.Fatal("initial attach did not return a layout")
	}
	if len(msg.Layout.Panes) != 1 {
		t.Fatalf("initial attach returned %d panes, want 1", len(msg.Layout.Panes))
	}
}

func TestServerHarnessSequentialLifecyclesKeepNextSessionAlive(t *testing.T) {
	t.Parallel()

	const iterations = 6

	for i := 0; i < iterations; i++ {
		t.Run(fmt.Sprintf("iter-%02d", i), func(t *testing.T) {
			h := newServerHarness(t)

			marker := fmt.Sprintf("ITER_%02d", i)
			h.sendKeys("pane-1", "echo "+marker, "Enter")
			h.waitFor("pane-1", marker)
			_ = h.generation()

			msg := h.attachAt(80, 24)
			if msg.Layout == nil {
				t.Fatal("transient attach did not return a layout")
			}

			h.splitV()

			msg = h.attachAt(80, 24)
			if msg.Layout == nil {
				t.Fatal("post-split attach did not return a layout")
			}
			if len(msg.Layout.Panes) != 2 {
				t.Fatalf("post-split attach returned %d panes, want 2", len(msg.Layout.Panes))
			}
		})
	}
}

func TestServerHarnessRunCmdKeepsWorkingWithoutSocketPath(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	sockPath := server.SocketPath(h.session)
	if err := os.Remove(sockPath); err != nil {
		t.Fatalf("Remove(%s): %v", sockPath, err)
	}

	out := h.runCmd("status")
	if !strings.Contains(out, "panes: 1 total") {
		t.Fatalf("status should still work through attached client after socket unlink, got:\n%s", out)
	}
}

func TestServerHarnessRunCmdPostReloadModeKeepsRemoteMutationsOnCLIPath(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.commandConnMu.Lock()
	h.postReloadProbes = true
	h.lastCommandConn = h.client.currentConn()
	h.commandConnMu.Unlock()

	sockPath := server.SocketPath(h.session)
	if err := os.Remove(sockPath); err != nil {
		t.Fatalf("Remove(%s): %v", sockPath, err)
	}

	if out := h.runCmd("status"); !strings.Contains(out, "panes: 1 total") {
		t.Fatalf("status should still work through attached client in post-reload mode, got:\n%s", out)
	}

	out := h.runCmd("send-keys", "pane-1@remote", "x")
	if !isCommandConnectError(out) {
		t.Fatalf("remote send-keys should still use the CLI path in post-reload mode, got:\n%s", out)
	}
}

func TestServerHarnessRunCmdPostReloadModeKeepsSafePollingOnCLIWhileAwaitingReconnect(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.commandConnMu.Lock()
	h.postReloadProbes = true
	h.awaitingReconnect = true
	h.lastCommandConn = h.client.currentConn()
	h.commandConnMu.Unlock()

	sockPath := server.SocketPath(h.session)
	if err := os.Remove(sockPath); err != nil {
		t.Fatalf("Remove(%s): %v", sockPath, err)
	}

	out := h.runCmd("status")
	if !isCommandConnectError(out) {
		t.Fatalf("safe polling should stay on the CLI path while awaiting reconnect, got:\n%s", out)
	}
}

func TestServerHarnessRunCmdPostReloadModeKeepsKnownPaneSendKeysOnAttachedClient(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.commandConnMu.Lock()
	h.postReloadProbes = true
	h.lastCommandConn = h.client.currentConn()
	h.commandConnMu.Unlock()

	sockPath := server.SocketPath(h.session)
	if err := os.Remove(sockPath); err != nil {
		t.Fatalf("Remove(%s): %v", sockPath, err)
	}

	out := h.runCmd("send-keys", "pane-1", "echo POST_RELOAD_ATTACHED", "Enter")
	if strings.Contains(out, "not found") || isCommandConnectError(out) {
		t.Fatalf("known-pane send-keys should stay on the attached client in post-reload mode, got:\n%s", out)
	}

	h.waitForTimeout("pane-1", "POST_RELOAD_ATTACHED", "5s")
}

func TestServerHarnessRunCmdFallsBackWhenHeadlessClientDetached(t *testing.T) {
	t.Parallel()

	h := newServerHarnessPersistent(t)
	h.client.close()
	h.client = nil

	out := h.runCmd("list")
	if !strings.Contains(out, "pane-1") {
		t.Fatalf("list should still work over the socket after detaching the headless client, got:\n%s", out)
	}
}

func TestServerHarnessLateGenerationAndAttachSurviveHeadlessClientDetach(t *testing.T) {
	t.Parallel()

	h := newServerHarnessPersistent(t)
	h.splitV()

	h.client.close()
	h.client = nil

	// generation() fatalf's if the server becomes unreachable after detach.
	_ = h.generation()

	msg := h.attachAt(80, 24)
	if msg.Layout == nil {
		t.Fatal("late attach did not return a layout after headless client detach")
	}
	if len(msg.Layout.Panes) != 2 {
		t.Fatalf("late attach returned %d panes, want 2", len(msg.Layout.Panes))
	}
}

func serverProcessMatchesSession(pid int, session string) bool {
	if pid == 0 || session == "" {
		return false
	}
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.TrimSpace(string(out)), "amux _server "+session)
}
