package remote

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/proto"
)

// HostConn manages a single SSH connection to a remote host and multiplexes
// all proxy panes on that host over a single amux wire protocol connection.
//
// Two types of connections are used:
//   - Persistent attach connection (amuxConn) for streaming pane output
//   - One-shot connections for commands (spawn, resize) via runCommand()
type HostConn struct {
	name      string
	config    config.Host
	buildHash string // local build hash for deploy decisions

	mu        sync.Mutex
	state     ConnState
	sshClient *ssh.Client
	amuxConn  net.Conn   // persistent attach connection for pane I/O
	writeMu   sync.Mutex // serializes writes to amuxConn

	// Pane ID mapping: local ↔ remote
	remoteToLocal map[uint32]uint32
	localToRemote map[uint32]uint32

	// Session name for the remote amux server (includes local hostname)
	sessionName string
	remoteUID   string // UID of the remote user (for socket path)

	// Callbacks back to the local server
	onPaneOutput  PaneOutputCallback
	onPaneExit    PaneExitCallback
	onStateChange StateChangeCallback
}

// NewHostConn creates a host connection (not yet connected).
func NewHostConn(name string, cfg config.Host, buildHash string,
	onOutput PaneOutputCallback, onExit PaneExitCallback, onStateChange StateChangeCallback) *HostConn {
	return &HostConn{
		name:          name,
		config:        cfg,
		buildHash:     buildHash,
		state:         Disconnected,
		remoteToLocal: make(map[uint32]uint32),
		localToRemote: make(map[uint32]uint32),
		onPaneOutput:  onOutput,
		onPaneExit:    onExit,
		onStateChange: onStateChange,
	}
}

// shouldDeploy returns true if auto-deploy should run for this host.
// Checks AMUX_NO_DEPLOY env var and the per-host deploy config opt-out.
func (hc *HostConn) shouldDeploy() bool {
	if os.Getenv("AMUX_NO_DEPLOY") == "1" {
		return false
	}
	if hc.config.Deploy != nil && !*hc.config.Deploy {
		return false
	}
	return hc.buildHash != ""
}

// State returns the current connection state. Thread-safe.
func (hc *HostConn) State() ConnState {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	return hc.state
}

func (hc *HostConn) setState(s ConnState) {
	hc.state = s
	if hc.onStateChange != nil {
		hc.onStateChange(hc.name, s)
	}
}

// EnsureConnected establishes the SSH connection and amux tunnel if not already connected.
func (hc *HostConn) EnsureConnected(sessionName string) error {
	hc.mu.Lock()
	if hc.state == Connected {
		hc.mu.Unlock()
		return nil
	}
	hc.setState(Connecting)
	hc.sessionName = sessionName
	hc.mu.Unlock()

	if err := hc.connect(sessionName); err != nil {
		hc.mu.Lock()
		hc.setState(Disconnected)
		hc.mu.Unlock()
		return err
	}

	hc.mu.Lock()
	hc.setState(Connected)
	hc.mu.Unlock()

	// Start reading pane output from the persistent attach connection
	go hc.readLoop()

	return nil
}

// connect establishes SSH and attaches to the remote amux server.
func (hc *HostConn) connect(sessionName string) error {
	sshCfg, err := hc.buildSSHConfig()
	if err != nil {
		return fmt.Errorf("building SSH config: %w", err)
	}

	addr := hc.config.Address
	if addr == "" {
		addr = hc.name
	}
	addr = normalizeAddr(addr)

	sshClient, err := ssh.Dial("tcp", addr, sshCfg)
	if err != nil {
		return fmt.Errorf("SSH dial: %w", err)
	}

	// Query the remote user's UID for socket path construction.
	// The remote amux server uses /tmp/amux-$UID/, and the remote UID
	// differs from the local UID (e.g., macOS UID 501 vs Linux UID 1000).
	remoteUID, err := sshOutput(sshClient, "id -u")
	if err != nil {
		sshClient.Close()
		return fmt.Errorf("querying remote UID: %w", err)
	}

	hc.mu.Lock()
	hc.remoteUID = remoteUID
	hc.mu.Unlock()

	// Deploy local binary to remote if needed (best-effort)
	if hc.shouldDeploy() {
		if err := DeployBinary(sshClient, hc.buildHash); err != nil {
			fmt.Fprintf(os.Stderr, "amux: deploy to %s: %v\n", hc.name, err)
		}
	}

	// Ensure remote amux server is running
	remoteSession := remoteSessionName(sessionName)
	if err := hc.ensureRemoteServer(sshClient, remoteSession); err != nil {
		sshClient.Close()
		return fmt.Errorf("starting remote server: %w", err)
	}

	// Persistent attach connection for streaming pane output.
	// Try Unix socket forwarding first (OpenSSH); fall back to TCP via
	// socat bridge (needed for Tailscale SSH which doesn't support
	// direct-streamlocal@openssh.com).
	remoteSock := hc.remoteSocketPath(remoteSession)
	amuxConn, err := hc.dialRemoteSocket(sshClient, remoteSock)
	if err != nil {
		sshClient.Close()
		return fmt.Errorf("dialing remote socket %s: %w", remoteSock, err)
	}

	// Attach to the remote server and wait for the first layout, which
	// guarantees the remote session has a window. Without the wait, a
	// subsequent runCommand("spawn") can race with handleAttach on the
	// remote server and fail with "no window".
	if err := attachAndWait(amuxConn, remoteSession, 10*time.Second); err != nil {
		amuxConn.Close()
		sshClient.Close()
		return err
	}

	hc.mu.Lock()
	hc.sshClient = sshClient
	hc.amuxConn = amuxConn
	hc.sessionName = remoteSession
	hc.mu.Unlock()

	return nil
}

// attachAndWait sends MsgTypeAttach and blocks until the remote server
// responds with a MsgTypeLayout, confirming the session window exists.
func attachAndWait(conn net.Conn, session string, timeout time.Duration) error {
	if err := proto.WriteMsg(conn, &proto.Message{
		Type:    proto.MsgTypeAttach,
		Session: session,
		Cols:    80,
		Rows:    24,
	}); err != nil {
		return fmt.Errorf("attaching to remote: %w", err)
	}
	if err := waitForLayout(conn, timeout); err != nil {
		return fmt.Errorf("waiting for remote layout: %w", err)
	}
	return nil
}

// waitForLayout reads messages from conn until a MsgTypeLayout arrives,
// confirming the remote server has a window. Non-layout messages are discarded.
func waitForLayout(conn net.Conn, timeout time.Duration) error {
	conn.SetReadDeadline(time.Now().Add(timeout))
	defer conn.SetReadDeadline(time.Time{})
	for {
		msg, err := proto.ReadMsg(conn)
		if err != nil {
			return err
		}
		if msg.Type == proto.MsgTypeLayout {
			return nil
		}
	}
}

// runCommand opens a one-shot connection to the remote amux server, sends a
// command, reads the result, and closes the connection. This avoids racing
// with the persistent readLoop on the attach connection.
func (hc *HostConn) runCommand(cmdName string, cmdArgs []string) (string, error) {
	hc.mu.Lock()
	sshClient := hc.sshClient
	session := hc.sessionName
	hc.mu.Unlock()

	if sshClient == nil {
		return "", fmt.Errorf("not connected")
	}

	remoteSock := hc.remoteSocketPath(session)
	conn, err := hc.dialRemoteSocket(sshClient, remoteSock)
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

// buildSSHConfig builds the SSH client configuration using agent auth and key files.
// When an identity_file is configured, it is tried first. This avoids issues
// where the SSH agent holds keys that Go's crypto/ssh can't sign with
// (e.g., macOS Keychain-backed keys), which would abort the handshake before
// the explicit key file is ever tried.
func (hc *HostConn) buildSSHConfig() (*ssh.ClientConfig, error) {
	var authMethods []ssh.AuthMethod

	// Load explicit identity file first (highest priority when configured),
	// then default key paths as fallback.
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

	// SSH agent as fallback — tried after explicit key files.
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

	return &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: proper host key verification
	}, nil
}

// ensureRemoteServer starts the remote amux server if it's not already running.
func (hc *HostConn) ensureRemoteServer(client *ssh.Client, sessionName string) error {
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()

	sockPath := hc.remoteSocketPath(sessionName)
	cmd := buildEnsureServerCmd(sockPath, sessionName)
	// Ignore errors — the server may already be running
	_ = sess.Run(cmd)
	return nil
}

// buildEnsureServerCmd returns the shell command that starts amux _server if
// the socket doesn't already exist. Tries ~/.local/bin/amux first (where deploy
// installs), then falls back to amux in PATH.
func buildEnsureServerCmd(sockPath, sessionName string) string {
	return fmt.Sprintf(
		`if [ ! -S %s ]; then AMUX=$(command -v ~/.local/bin/amux 2>/dev/null || command -v amux 2>/dev/null || echo amux); nohup "$AMUX" _server %s </dev/null >/dev/null 2>&1 & for i in 1 2 3 4 5 6 7 8 9 10; do [ -S %s ] && break; sleep 0.2; done; fi`,
		sockPath, sessionName, sockPath,
	)
}

// readLoop reads messages from the persistent attach connection and dispatches them.
func (hc *HostConn) readLoop() {
	for {
		hc.mu.Lock()
		conn := hc.amuxConn
		hc.mu.Unlock()

		if conn == nil {
			return
		}

		msg, err := proto.ReadMsg(conn)
		if err != nil {
			hc.handleDisconnect()
			return
		}

		switch msg.Type {
		case proto.MsgTypePaneOutput:
			hc.mu.Lock()
			localID, ok := hc.remoteToLocal[msg.PaneID]
			hc.mu.Unlock()
			if ok && hc.onPaneOutput != nil {
				hc.onPaneOutput(localID, msg.PaneData)
			}

		case proto.MsgTypeLayout:
			// Layout is managed locally. We only care about pane output.

		case proto.MsgTypeExit, proto.MsgTypeServerReload:
			// Connection ended or remote server is reloading — reconnect
			hc.handleDisconnect()
			return
		}
	}
}

// closeConnsLocked closes any open amux/SSH connections.
// Caller must hold hc.mu.
func (hc *HostConn) closeConnsLocked() {
	if hc.amuxConn != nil {
		hc.amuxConn.Close()
		hc.amuxConn = nil
	}
	if hc.sshClient != nil {
		hc.sshClient.Close()
		hc.sshClient = nil
	}
}

// handleDisconnect is called when the SSH/amux connection drops.
// It sets state to Reconnecting (preventing duplicate reconnect loops),
// closes stale connections, and starts the backoff loop.
func (hc *HostConn) handleDisconnect() {
	hc.mu.Lock()
	if hc.state != Connected {
		// Already disconnected or reconnecting — don't spawn another loop
		hc.mu.Unlock()
		return
	}
	hc.setState(Reconnecting)
	hc.closeConnsLocked()
	hc.mu.Unlock()

	// Start reconnection in background
	go hc.startReconnectLoop()
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

	// Parse: "Spawned remote-N in pane M\n"
	var remotePaneID uint32
	if idx := strings.LastIndex(output, "pane "); idx >= 0 {
		fmt.Sscanf(output[idx:], "pane %d", &remotePaneID)
	}
	if remotePaneID == 0 {
		return 0, fmt.Errorf("could not parse remote pane ID from: %s", output)
	}

	hc.mu.Lock()
	hc.remoteToLocal[remotePaneID] = localPaneID
	hc.localToRemote[localPaneID] = remotePaneID
	hc.mu.Unlock()

	return remotePaneID, nil
}

// SendInput sends keyboard input to a specific remote pane via the
// persistent attach connection using MsgTypeInputPane.
// Uses writeMu to serialize writes — proto.WriteMsg performs multiple
// Write calls (header + body) which must not interleave.
func (hc *HostConn) SendInput(localPaneID uint32, data []byte) error {
	hc.mu.Lock()
	conn := hc.amuxConn
	remotePaneID, ok := hc.localToRemote[localPaneID]
	hc.mu.Unlock()

	if !ok || conn == nil {
		return nil // silently drop input when disconnected
	}

	hc.writeMu.Lock()
	defer hc.writeMu.Unlock()
	return proto.WriteMsg(conn, &proto.Message{
		Type:     proto.MsgTypeInputPane,
		PaneID:   remotePaneID,
		PaneData: data,
	})
}

// SendResize notifies the remote server about a pane resize via one-shot command.
func (hc *HostConn) SendResize(localPaneID uint32, cols, rows int) error {
	hc.mu.Lock()
	_, ok := hc.localToRemote[localPaneID]
	hc.mu.Unlock()

	if !ok {
		return nil
	}

	_, err := hc.runCommand("resize-window", []string{
		fmt.Sprintf("%d", cols), fmt.Sprintf("%d", rows),
	})
	return err
}

// RemovePane cleans up the pane ID mapping.
func (hc *HostConn) RemovePane(localPaneID uint32) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	if remoteID, ok := hc.localToRemote[localPaneID]; ok {
		delete(hc.localToRemote, localPaneID)
		delete(hc.remoteToLocal, remoteID)
	}
}

// Disconnect closes the SSH connection and marks state as disconnected.
func (hc *HostConn) Disconnect() {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hc.closeConnsLocked()
	hc.setState(Disconnected)
}

// Reconnect attempts to re-establish the connection.
func (hc *HostConn) Reconnect(sessionName string) error {
	hc.Disconnect()
	return hc.EnsureConnected(sessionName)
}

// remoteSessionName returns the session name to use on the remote server.
// Includes the local hostname to prevent collisions from multiple local machines.
func remoteSessionName(localSessionName string) string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	return localSessionName + "@" + hostname
}

// remoteSocketPath returns the expected amux socket path on the remote host.
// Uses the cached remote UID (queried during connect).
func (hc *HostConn) remoteSocketPath(sessionName string) string {
	return fmt.Sprintf("/tmp/amux-%s/%s", hc.remoteUID, sessionName)
}

// dialRemoteSocket connects to the remote amux Unix socket. It tries direct
// Unix socket forwarding first (works with OpenSSH), then falls back to
// launching socat on the remote to bridge a TCP port to the socket (needed
// for Tailscale SSH which doesn't support direct-streamlocal@openssh.com).
func (hc *HostConn) dialRemoteSocket(client *ssh.Client, sockPath string) (net.Conn, error) {
	// Try direct Unix socket forwarding first
	conn, err := client.Dial("unix", sockPath)
	if err == nil {
		return conn, nil
	}

	// Fallback: start socat on the remote to bridge TCP→Unix socket.
	// Pick a high ephemeral port and have socat listen on localhost only.
	port, socatErr := hc.startSocatBridge(client, sockPath)
	if socatErr != nil {
		return nil, fmt.Errorf("unix dial failed (%w) and socat fallback failed (%w)", err, socatErr)
	}

	tcpConn, tcpErr := client.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if tcpErr != nil {
		return nil, fmt.Errorf("unix dial failed (%w) and TCP fallback failed (%w)", err, tcpErr)
	}
	return tcpConn, nil
}

// startSocatBridge launches socat on the remote to bridge a TCP port to the
// Unix socket. Returns the port number. The socat process exits when the
// TCP connection closes.
func (hc *HostConn) startSocatBridge(client *ssh.Client, sockPath string) (int, error) {
	// Find a free port and start socat
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
