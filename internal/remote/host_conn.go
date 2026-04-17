package remote

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	charmlog "github.com/charmbracelet/log"
	"golang.org/x/crypto/ssh"

	"github.com/weill-labs/amux/internal/auditlog"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/transport"
	transportssh "github.com/weill-labs/amux/internal/transport/ssh"
)

// HostConn manages a single SSH connection to a remote host and multiplexes
// all proxy panes on that host over a single amux wire protocol connection.
//
// All mutable state is owned by a single event-loop goroutine (the actor).
// Public methods send events to the actor and optionally wait for replies.
// This eliminates mutexes and the class of races they incompletely prevent
// (e.g., duplicate connect attempts, state tearing between lock/unlock gaps).
//
// Two types of connections are used:
//   - Persistent attach connection (amuxConn) for streaming pane output
//   - One-shot connections for commands (spawn, resize) via runCommand()
type HostConn struct {
	// Immutable after construction — safe to read from any goroutine.
	name      string
	config    config.Host
	buildHash string // local build hash for deploy decisions
	logger    *charmlog.Logger

	// Actor-owned state — accessed only from eventLoop goroutine.
	state      ConnState
	sshClient  *ssh.Client
	tr         transport.Transport
	amuxConn   net.Conn // persistent attach connection for pane I/O
	amuxReader *proto.Reader
	amuxWriter *proto.Writer

	// Pane ID mapping: local ↔ remote (actor-owned)
	remoteToLocal map[uint32]uint32
	localToRemote map[uint32]uint32

	// Session name for the remote amux server (includes local hostname)
	sessionName   string
	remoteUID     string // UID of the remote user (for socket path)
	connectAddr   string // normalized SSH address used by the current connection
	connectTarget transport.Target
	lastLayout    *proto.LayoutSnapshot
	takeoverMode  bool // true when established via takeover

	// Pending connect waiters — replied when connectDoneEvent arrives.
	pendingConnectReplies []chan error
	pendingInputs         []pendingPaneInput
	bufferPendingInputs   bool

	// Callbacks back to the local server (immutable after construction)
	onPaneOutput  PaneOutputCallback
	onPaneExit    PaneExitCallback
	onStateChange StateChangeCallback
	onLayout      LayoutChangeCallback

	// Event loop channels
	cmds      chan hostEvent
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// NewHostConn creates a host connection (not yet connected) and starts its
// event loop. Callers must call Close() when the connection is no longer needed.
func NewHostConn(name string, cfg config.Host, buildHash string,
	onOutput PaneOutputCallback, onExit PaneExitCallback, onStateChange StateChangeCallback) *HostConn {
	hc := &HostConn{
		name:          name,
		config:        cfg,
		buildHash:     buildHash,
		logger:        auditlog.Discard(),
		state:         Disconnected,
		remoteToLocal: make(map[uint32]uint32),
		localToRemote: make(map[uint32]uint32),
		onPaneOutput:  onOutput,
		onPaneExit:    onExit,
		onStateChange: onStateChange,
	}
	hc.startEventLoop()
	return hc
}

type pendingPaneInput struct {
	localPaneID uint32
	data        []byte
}

// shouldDeploy returns true if auto-deploy should run for this host.
// Reads only immutable fields — safe from any goroutine.
func (hc *HostConn) shouldDeploy() bool {
	if os.Getenv("AMUX_NO_DEPLOY") == "1" {
		return false
	}
	if hc.config.Deploy != nil && !*hc.config.Deploy {
		return false
	}
	return hc.buildHash != ""
}

// State returns the current connection state via the actor.
func (hc *HostConn) State() ConnState {
	reply := make(chan ConnState, 1)
	if !hc.enqueue(stateQuery{reply: reply}) {
		return Disconnected
	}
	select {
	case s := <-reply:
		return s
	case <-hc.done:
		return Disconnected
	}
}

// Layout returns the most recently observed remote layout snapshot.
func (hc *HostConn) Layout() *proto.LayoutSnapshot {
	reply := make(chan *proto.LayoutSnapshot, 1)
	if !hc.enqueue(layoutQuery{reply: reply}) {
		return nil
	}
	select {
	case layout := <-reply:
		return layout
	case <-hc.done:
		return nil
	}
}

// setState updates the state and fires the callback.
// Only called from the actor goroutine.
func (hc *HostConn) setState(s ConnState) {
	hc.state = s
	if hc.onStateChange != nil {
		hc.onStateChange(hc.name, s)
	}
}

// EnsureConnected establishes the SSH connection and amux tunnel if not already connected.
func (hc *HostConn) EnsureConnected(sessionName string) error {
	reply := make(chan error, 1)
	if !hc.enqueue(connectEvent{sessionName: sessionName, reply: reply}) {
		return errHostConnClosed
	}
	select {
	case err := <-reply:
		return err
	case <-hc.done:
		return errHostConnClosed
	}
}

// EnsureConnectedForTakeover establishes SSH+amux for a takeover pane.
// Unlike EnsureConnected, it skips ensureRemoteServer and waits for the socket.
func (hc *HostConn) EnsureConnectedForTakeover(sessionName, remoteUID, sshAddr string) error {
	reply := make(chan error, 1)
	if !hc.enqueue(connectTakeoverEvent{
		sessionName: sessionName,
		remoteUID:   remoteUID,
		sshAddr:     sshAddr,
		reply:       reply,
	}) {
		return errHostConnClosed
	}
	select {
	case err := <-reply:
		return err
	case <-hc.done:
		return errHostConnClosed
	}
}

// BeginInputBuffering preserves pane input until the persistent amux attach
// connection is ready. SSH takeover uses this so proxy panes can accept
// keystrokes immediately after they appear without dropping them on the floor.
func (hc *HostConn) BeginInputBuffering() {
	hc.enqueue(beginInputBufferingEvent{})
}

func (hc *HostConn) transportName() string {
	if hc.config.Transport != "" {
		return hc.config.Transport
	}
	return "ssh"
}

func (hc *HostConn) newTransport() (transport.Transport, error) {
	return transport.Get(hc.transportName(), hc.config)
}

func (hc *HostConn) targetForSession(sessionName string) transport.Target {
	target := transport.Target{
		Host:    hc.name,
		User:    hc.config.User,
		Session: sessionName,
	}
	if hc.connectAddr != "" {
		target.Host = hc.connectAddr
	}
	return target
}

// doConnect performs the SSH dial, deploy, server start, and amux attach.
// Runs outside the actor in a spawned goroutine. Only reads immutable fields;
// returns all results for the actor to apply.
func (hc *HostConn) doConnect(sessionName string) (*connectOutcome, error) {
	return hc.doConnectWithAddr(sessionName, "")
}

// doConnectWithAddr performs the SSH dial, deploy, server start, and amux attach
// using the supplied address. If addr is empty, the configured host address or
// HostConn name is used.
func (hc *HostConn) doConnectWithAddr(sessionName, addr string) (*connectOutcome, error) {
	target := hc.targetForSession(sessionName)
	if addr != "" {
		target.Host = addr
	}
	return hc.doConnectTarget(target, true, false)
}

// doConnectTakeover performs the SSH dial and amux attach for a takeover.
// Runs outside the actor in a spawned goroutine.
func (hc *HostConn) doConnectTakeover(sessionName, remoteUID, sshAddr string) (*connectOutcome, error) {
	_ = remoteUID
	target := hc.targetForSession(sessionName)
	if sshAddr != "" {
		target.Host = sshAddr
	}
	return hc.doConnectTarget(target, false, true)
}

func (hc *HostConn) doConnectTarget(target transport.Target, ensureServer bool, takeover bool) (*connectOutcome, error) {
	tr, err := hc.newTransport()
	if err != nil {
		return nil, fmt.Errorf("creating %s transport: %w", hc.transportName(), err)
	}

	if ensureServer && hc.shouldDeploy() {
		if err := tr.Deploy(context.Background(), target, hc.buildHash); err != nil {
			hc.logger.Warn("transport deploy failed",
				"event", "remote_deploy",
				"host", hc.name,
				"transport", tr.Name(),
				"error", err,
			)
		}
	}
	if ensureServer {
		if err := tr.EnsureServer(context.Background(), target, target.Session); err != nil {
			_ = tr.Close()
			if isImmediateTransportError(err) {
				return nil, err
			}
			return nil, fmt.Errorf("starting remote server: %w", err)
		}
	}

	amuxConn, err := hc.waitForDial(tr, target, 10*time.Second)
	if err != nil {
		_ = tr.Close()
		return nil, err
	}

	amuxReader := proto.NewReader(amuxConn)
	amuxWriter := proto.NewWriter(amuxConn)
	initialLayout, err := attachAndWaitLayout(amuxConn, amuxWriter, amuxReader, target.Session, 10*time.Second)
	if err != nil {
		amuxConn.Close()
		_ = tr.Close()
		return nil, err
	}

	return &connectOutcome{
		sshClient:     hc.sshClient,
		tr:            tr,
		amuxConn:      amuxConn,
		amuxReader:    amuxReader,
		amuxWriter:    amuxWriter,
		sessionName:   target.Session,
		connectAddr:   target.Host,
		connectTarget: target,
		initialLayout: initialLayout,
		takeover:      takeover,
	}, nil
}

func (hc *HostConn) waitForDial(tr transport.Transport, target transport.Target, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := tr.Dial(context.Background(), target)
		if err == nil {
			return conn, nil
		}
		if isImmediateTransportError(err) {
			return nil, err
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("dial timeout")
	}
	return nil, fmt.Errorf("dialing remote socket: %w", lastErr)
}

func isImmediateTransportError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SSH dial:") ||
		strings.Contains(msg, "building SSH config:") ||
		strings.Contains(msg, "querying remote UID:") ||
		strings.Contains(msg, "host key verification failed")
}

// Disconnect closes the SSH connection and marks state as disconnected.
func (hc *HostConn) Disconnect() {
	reply := make(chan struct{})
	if !hc.enqueue(disconnectEvent{reply: reply}) {
		return
	}
	<-reply
}

// Reconnect attempts to re-establish the connection.
func (hc *HostConn) Reconnect(sessionName string) error {
	reply := make(chan error, 1)
	if !hc.enqueue(reconnectCmd{sessionName: sessionName, reply: reply}) {
		return errHostConnClosed
	}
	select {
	case err := <-reply:
		return err
	case <-hc.done:
		return errHostConnClosed
	}
}

// RegisterPane registers a local-to-remote pane ID mapping for a takeover proxy
// pane. Must be called before EnsureConnectedForTakeover so that readLoop can
// route MsgTypePaneOutput to the correct local pane immediately after connecting.
func (hc *HostConn) RegisterPane(localPaneID, remotePaneID uint32) {
	hc.enqueue(registerPaneEvent{localPaneID: localPaneID, remotePaneID: remotePaneID})
}

// RemovePane cleans up the pane ID mapping.
func (hc *HostConn) RemovePane(localPaneID uint32) {
	hc.enqueue(removePaneEvent{localPaneID: localPaneID})
}

// CreateRemotePane creates a new pane on the remote server via a one-shot
// spawn command. Returns the remote pane ID.
func (hc *HostConn) CreateRemotePane(localPaneID uint32) (uint32, error) {
	output, err := hc.runCommand("spawn", []string{
		"--name", fmt.Sprintf("remote-%d", localPaneID),
	})
	if err != nil {
		return 0, err
	}

	remotePaneID, err := parseSpawnOutput(output)
	if err != nil {
		return 0, err
	}

	hc.RegisterPane(localPaneID, remotePaneID)
	return remotePaneID, nil
}

// SendInput sends keyboard input to a specific remote pane.
// The actor serializes writes to amuxConn, replacing the old writeMu.
func (hc *HostConn) SendInput(localPaneID uint32, data []byte) error {
	hc.enqueue(sendInputEvent{localPaneID: localPaneID, data: data})
	return nil
}

// SendResize notifies the remote server about a pane resize via one-shot command.
func (hc *HostConn) SendResize(localPaneID uint32, cols, rows int) error {
	reply := make(chan bool, 1)
	if !hc.enqueue(paneExistsQuery{localPaneID: localPaneID, reply: reply}) {
		return nil
	}
	select {
	case exists := <-reply:
		if !exists {
			return nil
		}
	case <-hc.done:
		return nil
	}

	_, err := hc.runCommand("resize-window", []string{
		fmt.Sprintf("%d", cols), fmt.Sprintf("%d", rows),
	})
	return err
}

// queryConnInfo retrieves connection info from the actor for one-shot commands.
func (hc *HostConn) queryConnInfo() connInfoResult {
	reply := make(chan connInfoResult, 1)
	if !hc.enqueue(connInfoQuery{reply: reply}) {
		return connInfoResult{}
	}
	select {
	case info := <-reply:
		return info
	case <-hc.done:
		return connInfoResult{}
	}
}

func (hc *HostConn) queryRemotePaneID(localPaneID uint32) remotePaneIDResult {
	reply := make(chan remotePaneIDResult, 1)
	if !hc.enqueue(remotePaneIDQuery{localPaneID: localPaneID, reply: reply}) {
		return remotePaneIDResult{}
	}
	select {
	case result := <-reply:
		return result
	case <-hc.done:
		return remotePaneIDResult{}
	}
}

// runCommand opens a one-shot connection to the remote amux server, sends a
// command, reads the result, and closes the connection. This avoids racing
// with the persistent readLoop on the attach connection.
func (hc *HostConn) runCommand(cmdName string, cmdArgs []string) (string, error) {
	info := hc.queryConnInfo()
	if info.sessionName == "" {
		return "", fmt.Errorf("not connected")
	}
	if info.sshClient != nil {
		remoteSock := socketPath(info.remoteUID, info.sessionName)
		return hc.runSocketCommand(info.sshClient, remoteSock, cmdName, cmdArgs)
	}

	target := info.target
	target.Session = info.sessionName
	return hc.runTransportCommand(target, cmdName, cmdArgs)
}

func (hc *HostConn) runSocketCommand(client *ssh.Client, sockPath, cmdName string, cmdArgs []string) (string, error) {
	conn, err := hc.dialRemoteSocket(client, sockPath)
	if err != nil {
		return "", fmt.Errorf("dialing remote socket: %w", err)
	}
	defer conn.Close()

	if err := proto.WriteMsg(conn, &proto.Message{
		Type:    proto.MsgTypeCommand,
		CmdName: cmdName,
		CmdArgs: cmdArgs,
	}); err != nil {
		return "", err
	}

	reply, err := proto.ReadMsg(conn)
	if err != nil {
		return "", err
	}
	if reply.CmdErr != "" {
		return "", fmt.Errorf("%s", reply.CmdErr)
	}
	return reply.CmdOutput, nil
}

func (hc *HostConn) runTransportCommand(target transport.Target, cmdName string, cmdArgs []string) (string, error) {
	tr, err := hc.newTransport()
	if err != nil {
		return "", fmt.Errorf("creating %s transport: %w", hc.transportName(), err)
	}
	defer tr.Close()

	conn, err := tr.Dial(context.Background(), target)
	if err != nil {
		return "", fmt.Errorf("dialing remote socket: %w", err)
	}
	defer conn.Close()

	if err := proto.WriteMsg(conn, &proto.Message{
		Type:    proto.MsgTypeCommand,
		CmdName: cmdName,
		CmdArgs: cmdArgs,
	}); err != nil {
		return "", err
	}

	reply, err := proto.ReadMsg(conn)
	if err != nil {
		return "", err
	}
	if reply.CmdErr != "" {
		return "", fmt.Errorf("%s", reply.CmdErr)
	}
	return reply.CmdOutput, nil
}

// KillPane forwards a kill command to the mapped remote pane.
func (hc *HostConn) KillPane(localPaneID uint32, cleanup bool, timeout time.Duration) error {
	remotePane := hc.queryRemotePaneID(localPaneID)
	if !remotePane.ok {
		return fmt.Errorf("remote mapping for pane %d not found", localPaneID)
	}

	args := make([]string, 0, 4)
	if cleanup {
		args = append(args, "--cleanup", "--timeout", timeout.String())
	}
	args = append(args, fmt.Sprintf("%d", remotePane.remotePaneID))
	_, err := hc.runCommand("kill", args)
	return err
}

// readLoop reads messages from the persistent attach connection and dispatches
// them through the actor. Runs in its own goroutine; exits when conn is closed
// or returns an error.
func (hc *HostConn) readLoop(reader *proto.Reader) {
	for {
		msg, err := reader.ReadMsg()
		if err != nil {
			hc.enqueue(readDisconnectEvent{})
			return
		}

		switch msg.Type {
		case proto.MsgTypePaneOutput:
			hc.enqueue(readPaneOutputEvent{
				remotePaneID: msg.PaneID,
				data:         msg.PaneData,
			})

		case proto.MsgTypeLayout:
			hc.enqueue(readLayoutEvent{layout: msg.Layout})

		case proto.MsgTypeExit, proto.MsgTypeServerReload:
			hc.enqueue(readDisconnectEvent{})
			return
		}
	}
}

// closeConns closes any open amux/SSH connections.
// Only called from the actor goroutine.
func (hc *HostConn) closeConns() {
	if hc.amuxConn != nil {
		hc.amuxConn.Close()
		hc.amuxConn = nil
	}
	hc.amuxReader = nil
	hc.amuxWriter = nil
	if hc.sshClient != nil {
		hc.sshClient.Close()
		hc.sshClient = nil
	}
	if hc.tr != nil {
		_ = hc.tr.Close()
		hc.tr = nil
	}
}

func (hc *HostConn) sendInputNow(localPaneID uint32, data []byte) error {
	remotePaneID, ok := hc.localToRemote[localPaneID]
	if !ok || hc.amuxWriter == nil {
		return nil
	}
	return hc.amuxWriter.WriteMsg(&proto.Message{
		Type:     proto.MsgTypeInputPane,
		PaneID:   remotePaneID,
		PaneData: data,
	})
}

func (hc *HostConn) flushPendingInputs() {
	if hc.amuxConn == nil || len(hc.pendingInputs) == 0 {
		return
	}

	pending := hc.pendingInputs
	hc.pendingInputs = nil
	for i, input := range pending {
		if err := hc.sendInputNow(input.localPaneID, input.data); err != nil {
			hc.pendingInputs = append([]pendingPaneInput{input}, pending[i+1:]...)
			hc.logger.Warn("remote input write failed", "event", "remote_input", "host", hc.name, "error", err)
			readDisconnectEvent{}.handle(hc)
			return
		}
	}
}

// --- Pure/immutable helpers (safe from any goroutine) ---

// attachAndWait sends MsgTypeAttach and blocks until the remote server
// responds with a MsgTypeLayout, confirming the session window exists.
func attachAndWait(conn net.Conn, writer *proto.Writer, reader *proto.Reader, session string, timeout time.Duration) error {
	_, err := attachAndWaitLayout(conn, writer, reader, session, timeout)
	return err
}

func attachAndWaitLayout(conn net.Conn, writer *proto.Writer, reader *proto.Reader, session string, timeout time.Duration) (*proto.LayoutSnapshot, error) {
	if err := writer.WriteMsg(&proto.Message{
		Type:       proto.MsgTypeAttach,
		Session:    session,
		Cols:       80,
		Rows:       24,
		AttachMode: proto.AttachModeNonInteractive,
	}); err != nil {
		return nil, fmt.Errorf("attaching to remote: %w", err)
	}
	layout, err := waitForLayoutSnapshot(conn, reader, timeout)
	if err != nil {
		return nil, fmt.Errorf("waiting for remote layout: %w", err)
	}
	return layout, nil
}

// waitForLayout reads messages from conn until a usable MsgTypeLayout arrives,
// confirming the remote server has an active window with at least one pane.
// Non-layout or unusable layout messages are discarded until timeout.
func waitForLayout(conn net.Conn, reader *proto.Reader, timeout time.Duration) error {
	_, err := waitForLayoutSnapshot(conn, reader, timeout)
	return err
}

func waitForLayoutSnapshot(conn net.Conn, reader *proto.Reader, timeout time.Duration) (layout *proto.LayoutSnapshot, err error) {
	deadlineErr := conn.SetReadDeadline(time.Now().Add(timeout))
	if deadlineErr == nil {
		defer func() {
			clearErr := conn.SetReadDeadline(time.Time{})
			if err == nil && clearErr != nil && !isNoDeadlineError(clearErr) {
				err = clearErr
			}
		}()
		return readUntilReadyLayout(reader)
	}

	if !isNoDeadlineError(deadlineErr) {
		return nil, deadlineErr
	}

	result := make(chan struct {
		layout *proto.LayoutSnapshot
		err    error
	}, 1)
	go func() {
		nextLayout, nextErr := readUntilReadyLayout(reader)
		result <- struct {
			layout *proto.LayoutSnapshot
			err    error
		}{layout: nextLayout, err: nextErr}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case res := <-result:
		return res.layout, res.err
	case <-timer.C:
		return nil, os.ErrDeadlineExceeded
	}
}

func readUntilReadyLayout(reader *proto.Reader) (*proto.LayoutSnapshot, error) {
	for {
		msg, err := reader.ReadMsg()
		if err != nil {
			return nil, err
		}
		if msg.Type == proto.MsgTypeLayout && msg.Layout != nil {
			return msg.Layout, nil
		}
	}
}

func layoutReady(layout *proto.LayoutSnapshot) bool {
	if layout == nil {
		return false
	}

	if len(layout.Windows) == 0 {
		return layout.ActivePaneID != 0 && len(layout.Panes) > 0
	}
	if layout.ActiveWindowID == 0 {
		return false
	}
	for _, win := range layout.Windows {
		if win.ID != layout.ActiveWindowID {
			continue
		}
		return win.ActivePaneID != 0 && len(win.Panes) > 0
	}
	return false
}

func isNoDeadlineError(err error) bool {
	return errors.Is(err, os.ErrNoDeadline) || strings.Contains(err.Error(), "deadline not supported")
}

// parseSpawnOutput extracts the pane ID from "Spawned remote-N in pane M\n".
func parseSpawnOutput(output string) (uint32, error) {
	var id uint32
	if idx := strings.LastIndex(output, "pane "); idx >= 0 {
		if _, err := fmt.Sscanf(output[idx:], "pane %d", &id); err != nil {
			return 0, fmt.Errorf("parsing remote pane ID from %q: %w", output[idx:], err)
		}
	}
	if id == 0 {
		return 0, fmt.Errorf("could not parse remote pane ID from: %s", output)
	}
	return id, nil
}

// buildSSHConfig builds the SSH client configuration using agent auth and key files.
func (hc *HostConn) buildSSHConfig() (*ssh.ClientConfig, error) {
	cfg, err := transportssh.BuildSSHConfig(hc.config.User, hc.config.IdentityFile)
	if err != nil {
		return nil, err
	}
	if os.Getenv("AMUX_SSH_INSECURE") != "1" {
		cfg.HostKeyCallback = hostKeyCallback("", hc.logger)
	}
	return cfg, nil
}

// ensureRemoteServer starts the remote amux server if it's not already running.
func ensureRemoteServer(client *ssh.Client, sockPath, sessionName string) error {
	return transportssh.EnsureRemoteServer(client, sockPath, sessionName)
}

// socketPath returns the expected amux socket path on the remote host.
func socketPath(remoteUID, sessionName string) string {
	return transportssh.RemoteSocketPath(remoteUID, sessionName)
}

// ManagedSessionName returns the session name to use on the remote server.
func ManagedSessionName(localSessionName string) string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	return localSessionName + "@" + hostname
}

// waitForSocket polls via SSH until sockPath exists on the remote host or timeout expires.
func waitForSocket(client *ssh.Client, sockPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := sshOutput(client, fmt.Sprintf("test -S %s && echo ok", sockPath))
		if err == nil && out == "ok" {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("socket %s did not appear within %v", sockPath, timeout)
}

// dialRemoteSocket connects to the remote amux Unix socket.
func (hc *HostConn) dialRemoteSocket(client *ssh.Client, sockPath string) (net.Conn, error) {
	return transportssh.DialRemoteSocket(client, sockPath)
}

// normalizeAddr ensures the address has a port, defaulting to :22.
func normalizeAddr(addr string) string {
	return transportssh.NormalizeAddr(addr)
}

func sshOutput(client *ssh.Client, cmd string) (string, error) {
	return transportssh.SSHOutput(client, cmd)
}

func addrOrFallback(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func normalizedDialAddr(hostName string, candidates ...string) string {
	addr := addrOrFallback(candidates...)
	if addr == "" {
		addr = hostName
	}
	return normalizeAddr(addr)
}
