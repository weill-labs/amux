package remote

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	charmlog "github.com/charmbracelet/log"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/weill-labs/amux/internal/auditlog"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/proto"
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
	state     ConnState
	sshClient *ssh.Client
	amuxConn  net.Conn // persistent attach connection for pane I/O

	// Pane ID mapping: local ↔ remote (actor-owned)
	remoteToLocal map[uint32]uint32
	localToRemote map[uint32]uint32

	// Session name for the remote amux server (includes local hostname)
	sessionName  string
	remoteUID    string // UID of the remote user (for socket path)
	connectAddr  string // normalized SSH address used by the current connection
	takeoverMode bool   // true when established via takeover

	// Pending connect waiters — replied when connectDoneEvent arrives.
	pendingConnectReplies []chan error
	pendingInputs         []pendingPaneInput
	bufferPendingInputs   bool

	// Callbacks back to the local server (immutable after construction)
	onPaneOutput  PaneOutputCallback
	onPaneExit    PaneExitCallback
	onStateChange StateChangeCallback

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
	sshCfg, err := hc.buildSSHConfig()
	if err != nil {
		return nil, fmt.Errorf("building SSH config: %w", err)
	}

	addr = normalizedDialAddr(hc.name, addr, hc.config.Address)

	sshClient, err := ssh.Dial("tcp", addr, sshCfg)
	if err != nil {
		return nil, fmt.Errorf("SSH dial: %w", err)
	}

	// Query the remote user's UID for socket path construction.
	remoteUID, err := sshOutput(sshClient, "id -u")
	if err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("querying remote UID: %w", err)
	}

	// Deploy local binary to remote if needed (best-effort)
	if hc.shouldDeploy() {
		if err := DeployBinary(sshClient, hc.buildHash); err != nil {
			fmt.Fprintf(os.Stderr, "amux: deploy to %s: %v\n", hc.name, err)
		}
	}

	// Ensure remote amux server is running
	remoteSession := ManagedSessionName(sessionName)
	sockPath := socketPath(remoteUID, remoteSession)
	if err := ensureRemoteServer(sshClient, sockPath, remoteSession); err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("starting remote server: %w", err)
	}

	// Persistent attach connection for streaming pane output.
	amuxConn, err := hc.dialRemoteSocket(sshClient, sockPath)
	if err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("dialing remote socket %s: %w", sockPath, err)
	}

	if err := attachAndWait(amuxConn, remoteSession, 10*time.Second); err != nil {
		amuxConn.Close()
		sshClient.Close()
		return nil, err
	}
	if err := hc.validateRemoteSessionHasPanes(sshClient, sockPath, remoteSession); err != nil {
		amuxConn.Close()
		sshClient.Close()
		return nil, err
	}

	return &connectOutcome{
		sshClient:   sshClient,
		amuxConn:    amuxConn,
		sessionName: remoteSession,
		remoteUID:   remoteUID,
		connectAddr: addr,
	}, nil
}

// doConnectTakeover performs the SSH dial and amux attach for a takeover.
// Runs outside the actor in a spawned goroutine.
func (hc *HostConn) doConnectTakeover(sessionName, remoteUID, sshAddr string) (*connectOutcome, error) {
	sshCfg, err := hc.buildSSHConfig()
	if err != nil {
		return nil, fmt.Errorf("building SSH config: %w", err)
	}

	sshAddr = normalizeAddr(sshAddr)

	sshClient, err := ssh.Dial("tcp", sshAddr, sshCfg)
	if err != nil {
		return nil, fmt.Errorf("SSH dial %s: %w", sshAddr, err)
	}

	remoteSock := socketPath(remoteUID, sessionName)
	if err := waitForSocket(sshClient, remoteSock, 5*time.Second); err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("waiting for remote socket %s: %w", remoteSock, err)
	}

	amuxConn, err := hc.dialRemoteSocket(sshClient, remoteSock)
	if err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("dialing remote socket %s: %w", remoteSock, err)
	}

	if err := attachAndWait(amuxConn, sessionName, 10*time.Second); err != nil {
		amuxConn.Close()
		sshClient.Close()
		return nil, err
	}
	if err := hc.validateRemoteSessionHasPanes(sshClient, remoteSock, sessionName); err != nil {
		amuxConn.Close()
		sshClient.Close()
		return nil, err
	}

	return &connectOutcome{
		sshClient:   sshClient,
		amuxConn:    amuxConn,
		sessionName: sessionName,
		remoteUID:   remoteUID,
		connectAddr: sshAddr,
		takeover:    true,
	}, nil
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
	if info.sshClient == nil {
		return "", fmt.Errorf("not connected")
	}

	remoteSock := socketPath(info.remoteUID, info.sessionName)
	return hc.runSocketCommand(info.sshClient, remoteSock, cmdName, cmdArgs)
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

func (hc *HostConn) validateRemoteSessionHasPanes(client *ssh.Client, sockPath, sessionName string) error {
	output, err := hc.runSocketCommand(client, sockPath, "list", nil)
	if err != nil {
		return fmt.Errorf("listing remote panes for %s: %w", sessionName, err)
	}
	if strings.TrimSpace(output) == "No panes." {
		return fmt.Errorf("remote session %q has no panes", sessionName)
	}
	return nil
}

// readLoop reads messages from the persistent attach connection and dispatches
// them through the actor. Runs in its own goroutine; exits when conn is closed
// or returns an error.
func (hc *HostConn) readLoop(conn net.Conn) {
	for {
		msg, err := proto.ReadMsg(conn)
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
	if hc.sshClient != nil {
		hc.sshClient.Close()
		hc.sshClient = nil
	}
}

func (hc *HostConn) sendInputNow(localPaneID uint32, data []byte) {
	remotePaneID, ok := hc.localToRemote[localPaneID]
	if !ok || hc.amuxConn == nil {
		return
	}
	proto.WriteMsg(hc.amuxConn, &proto.Message{
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
	for _, input := range pending {
		hc.sendInputNow(input.localPaneID, input.data)
	}
}

// --- Pure/immutable helpers (safe from any goroutine) ---

// attachAndWait sends MsgTypeAttach and blocks until the remote server
// responds with a MsgTypeLayout, confirming the session window exists.
func attachAndWait(conn net.Conn, session string, timeout time.Duration) error {
	if err := proto.WriteMsg(conn, &proto.Message{
		Type:       proto.MsgTypeAttach,
		Session:    session,
		Cols:       80,
		Rows:       24,
		AttachMode: proto.AttachModeNonInteractive,
	}); err != nil {
		return fmt.Errorf("attaching to remote: %w", err)
	}
	if err := waitForLayout(conn, timeout); err != nil {
		return fmt.Errorf("waiting for remote layout: %w", err)
	}
	return nil
}

// waitForLayout reads messages from conn until a usable MsgTypeLayout arrives,
// confirming the remote server has an active window with at least one pane.
// Non-layout or unusable layout messages are discarded until timeout.
func waitForLayout(conn net.Conn, timeout time.Duration) error {
	conn.SetReadDeadline(time.Now().Add(timeout))
	defer conn.SetReadDeadline(time.Time{})
	for {
		msg, err := proto.ReadMsg(conn)
		if err != nil {
			return err
		}
		if msg.Type == proto.MsgTypeLayout && layoutReady(msg.Layout) {
			return nil
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

// parseSpawnOutput extracts the pane ID from "Spawned remote-N in pane M\n".
func parseSpawnOutput(output string) (uint32, error) {
	var id uint32
	if idx := strings.LastIndex(output, "pane "); idx >= 0 {
		fmt.Sscanf(output[idx:], "pane %d", &id)
	}
	if id == 0 {
		return 0, fmt.Errorf("could not parse remote pane ID from: %s", output)
	}
	return id, nil
}

// buildSSHConfig builds the SSH client configuration using agent auth and key files.
func (hc *HostConn) buildSSHConfig() (*ssh.ClientConfig, error) {
	var authMethods []ssh.AuthMethod

	keyPaths := []string{
		os.ExpandEnv("$HOME/.ssh/id_ed25519"),
		os.ExpandEnv("$HOME/.ssh/id_rsa"),
	}
	if hc.config.IdentityFile != "" {
		keyPaths = append([]string{hc.config.IdentityFile}, keyPaths...)
	}
	for _, keyPath := range keyPaths {
		key, err := os.ReadFile(keyPath)
		if err != nil {
			continue
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			continue
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			agentClient := agent.NewClient(conn)
			authMethods = append(authMethods, ssh.PublicKeysCallback(agentClient.Signers))
		}
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no SSH auth methods available (no agent, no key files)")
	}

	user := hc.config.User
	if user == "" {
		user = "ubuntu"
	}

	var hkCallback ssh.HostKeyCallback
	if os.Getenv("AMUX_SSH_INSECURE") == "1" {
		hkCallback = ssh.InsecureIgnoreHostKey()
	} else {
		hkCallback = hostKeyCallback("")
	}

	return &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: hkCallback,
	}, nil
}

// ensureRemoteServer starts the remote amux server if it's not already running.
func ensureRemoteServer(client *ssh.Client, sockPath, sessionName string) error {
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	cmd := buildEnsureServerCmd(sockPath, sessionName)
	return sess.Run(cmd)
}

// buildEnsureServerCmd returns the shell command that starts amux _server if
// the socket doesn't already exist.
func buildEnsureServerCmd(sockPath, sessionName string) string {
	return fmt.Sprintf(
		`if [ ! -S %s ]; then AMUX=${AMUX_BIN:-$(command -v ~/.local/bin/amux 2>/dev/null || command -v amux 2>/dev/null || echo amux)}; "$AMUX" install-terminfo || exit 1; nohup "$AMUX" _server %s </dev/null >/dev/null 2>&1 & for i in 1 2 3 4 5 6 7 8 9 10; do [ -S %s ] && break; sleep 0.2; done; fi`,
		sockPath, sessionName, sockPath,
	)
}

// socketPath returns the expected amux socket path on the remote host.
func socketPath(remoteUID, sessionName string) string {
	return fmt.Sprintf("/tmp/amux-%s/%s", remoteUID, sessionName)
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
	conn, err := client.Dial("unix", sockPath)
	if err == nil {
		return conn, nil
	}

	tcpConn, tcpErr := hc.dialRemoteSocketTCP(client, sockPath)
	if tcpErr != nil {
		return nil, fmt.Errorf("unix dial failed (%w) and TCP fallback failed (%w)", err, tcpErr)
	}
	return tcpConn, nil
}

func (hc *HostConn) dialRemoteSocketTCP(client *ssh.Client, sockPath string) (net.Conn, error) {
	port, err := hc.startSocatBridge(client, sockPath)
	if err != nil {
		return nil, err
	}

	tcpConn, err := client.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}
	return tcpConn, nil
}

// startSocatBridge launches socat on the remote to bridge a TCP port to the
// Unix socket.
func (hc *HostConn) startSocatBridge(client *ssh.Client, sockPath string) (int, error) {
	out, err := sshOutput(client, fmt.Sprintf(
		`port=$(python3 -c "import socket; s=socket.socket(); s.bind(('',0)); print(s.getsockname()[1]); s.close()" 2>/dev/null || shuf -i 49152-65535 -n 1); `+
			`nohup socat TCP-LISTEN:$port,bind=127.0.0.1,fork,reuseaddr UNIX-CONNECT:%s </dev/null >/dev/null 2>&1 & `+
			`sleep 0.3; echo $port`, sockPath))
	if err != nil {
		return 0, fmt.Errorf("starting socat: %w", err)
	}

	var port int
	fmt.Sscanf(out, "%d", &port)
	if port == 0 {
		return 0, fmt.Errorf("could not parse socat port from: %s", out)
	}
	return port, nil
}

// hasPort returns true if the address already includes a port.
func hasPort(addr string) bool {
	_, _, err := net.SplitHostPort(addr)
	return err == nil
}

// normalizeAddr ensures the address has a port, defaulting to :22.
func normalizeAddr(addr string) string {
	if !hasPort(addr) {
		return addr + ":22"
	}
	return addr
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
