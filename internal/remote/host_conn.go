package remote

import (
	"fmt"
	"net"
	"os"
	"sync"

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
	name   string
	config config.Host

	mu        sync.Mutex
	state     ConnState
	sshClient *ssh.Client
	amuxConn  net.Conn    // persistent attach connection for pane I/O
	writeMu   sync.Mutex  // serializes writes to amuxConn

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
func NewHostConn(name string, cfg config.Host,
	onOutput PaneOutputCallback, onExit PaneExitCallback, onStateChange StateChangeCallback) *HostConn {
	return &HostConn{
		name:          name,
		config:        cfg,
		state:         Disconnected,
		remoteToLocal: make(map[uint32]uint32),
		localToRemote: make(map[uint32]uint32),
		onPaneOutput:  onOutput,
		onPaneExit:    onExit,
		onStateChange: onStateChange,
	}
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
	if !hasPort(addr) {
		addr = addr + ":22"
	}

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

	// Ensure remote amux server is running
	remoteSession := remoteSessionName(sessionName)
	if err := hc.ensureRemoteServer(sshClient, remoteSession); err != nil {
		sshClient.Close()
		return fmt.Errorf("starting remote server: %w", err)
	}

	// Persistent attach connection for streaming pane output
	remoteSock := hc.remoteSocketPath(remoteSession)
	amuxConn, err := sshClient.Dial("unix", remoteSock)
	if err != nil {
		sshClient.Close()
		return fmt.Errorf("dialing remote socket %s: %w", remoteSock, err)
	}

	if err := proto.WriteMsg(amuxConn, &proto.Message{
		Type:    proto.MsgTypeAttach,
		Session: remoteSession,
		Cols:    80,
		Rows:    24,
	}); err != nil {
		amuxConn.Close()
		sshClient.Close()
		return fmt.Errorf("attaching to remote: %w", err)
	}

	hc.mu.Lock()
	hc.sshClient = sshClient
	hc.amuxConn = amuxConn
	hc.sessionName = remoteSession
	hc.mu.Unlock()

	return nil
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
	conn, err := sshClient.Dial("unix", remoteSock)
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
func (hc *HostConn) buildSSHConfig() (*ssh.ClientConfig, error) {
	var authMethods []ssh.AuthMethod

	// Try SSH agent first
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			agentClient := agent.NewClient(conn)
			authMethods = append(authMethods, ssh.PublicKeysCallback(agentClient.Signers))
		}
	}

	// Try key files
	for _, keyPath := range []string{
		os.ExpandEnv("$HOME/.ssh/id_ed25519"),
		os.ExpandEnv("$HOME/.ssh/id_rsa"),
	} {
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
	cmd := fmt.Sprintf(
		`if [ ! -S %s ]; then nohup amux _server %s </dev/null >/dev/null 2>&1 & sleep 0.5; fi`,
		sockPath, sessionName,
	)
	// Ignore errors — the server may already be running
	_ = sess.Run(cmd)
	return nil
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
	fmt.Sscanf(output, "Spawned remote-%*d in pane %d", &remotePaneID)
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

// hasPort returns true if the address already includes a port.
func hasPort(addr string) bool {
	_, _, err := net.SplitHostPort(addr)
	return err == nil
}
