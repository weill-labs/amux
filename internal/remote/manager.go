// Package remote manages SSH connections to remote amux servers and proxies
// pane I/O between the local and remote instances. The local amux server acts
// as a client to each remote amux server, reusing the existing wire protocol.
package remote

import (
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/weill-labs/amux/internal/config"
)

// ConnState represents the connection state of a remote host.
type ConnState string

const (
	Disconnected ConnState = "disconnected"
	Connecting   ConnState = "connecting"
	Connected    ConnState = "connected"
	Reconnecting ConnState = "reconnecting"
)

// PaneCreatedCallback is called when a remote pane is created and ready.
// localPaneID is the local pane ID, remotePaneID is the ID on the remote server.
type PaneCreatedCallback func(localPaneID, remotePaneID uint32)

// PaneOutputCallback is called when output arrives from a remote pane.
type PaneOutputCallback func(localPaneID uint32, data []byte)

// PaneExitCallback is called when a remote pane exits.
type PaneExitCallback func(localPaneID uint32, reason string)

// StateChangeCallback is called when a host's connection state changes.
type StateChangeCallback func(hostName string, state ConnState)

// Manager coordinates all remote host connections. It maps local pane IDs
// to their remote counterparts and routes I/O through the appropriate HostConn.
type Manager struct {
	mu        sync.Mutex
	hosts     map[string]*HostConn // keyed by config host name
	cfg       *config.Config
	buildHash string // local build hash for deploy decisions

	// localToHost maps local pane ID → host name for routing input
	localToHost map[uint32]string

	// Callbacks wired by the server
	onPaneOutput  PaneOutputCallback
	onPaneExit    PaneExitCallback
	onStateChange StateChangeCallback
}

// NewManager creates a remote host manager with the given config.
func NewManager(cfg *config.Config, buildHash string) *Manager {
	return &Manager{
		hosts:       make(map[string]*HostConn),
		cfg:         cfg,
		buildHash:   buildHash,
		localToHost: make(map[uint32]string),
	}
}

// Config returns the underlying config.
func (m *Manager) Config() *config.Config {
	return m.cfg
}

// DeployToAddress deploys the local binary to a remote host via a temporary SSH
// connection. Used for post-takeover deployment when no persistent HostConn exists.
func (m *Manager) DeployToAddress(hostName, sshAddr, sshUser string) {
	if m.buildHash == "" {
		return
	}

	hostCfg, ok := m.cfg.Hosts[hostName]
	if !ok {
		hostCfg = config.Host{Type: "remote", Address: sshAddr, User: sshUser}
	}

	hc := NewHostConn("deploy-tmp", hostCfg, m.buildHash, nil, nil, nil)
	defer hc.Close()
	if !hc.shouldDeploy() {
		return
	}

	sshCfg, err := hc.buildSSHConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux: deploy to %s: %v\n", hostName, err)
		return
	}

	client, err := ssh.Dial("tcp", normalizeAddr(sshAddr), sshCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux: deploy to %s: SSH dial: %v\n", hostName, err)
		return
	}
	defer client.Close()

	if err := DeployBinary(client, m.buildHash); err != nil {
		fmt.Fprintf(os.Stderr, "amux: deploy to %s: %v\n", hostName, err)
	}
}

// SetCallbacks configures the callbacks the manager uses to communicate
// with the local server. Must be called before creating any panes.
func (m *Manager) SetCallbacks(
	onOutput PaneOutputCallback,
	onExit PaneExitCallback,
	onStateChange StateChangeCallback,
) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onPaneOutput = onOutput
	m.onPaneExit = onExit
	m.onStateChange = onStateChange
}

// CreatePane establishes or reuses an SSH connection to the named host and
// creates a new pane on the remote amux server. Returns the remote pane ID.
// The localPaneID is used for routing output back to the correct local pane.
func (m *Manager) CreatePane(hostName string, localPaneID uint32, sessionName string) (uint32, error) {
	m.mu.Lock()

	host, ok := m.cfg.Hosts[hostName]
	if !ok {
		m.mu.Unlock()
		return 0, fmt.Errorf("host %q not found in config", hostName)
	}
	if host.Type == "local" {
		m.mu.Unlock()
		return 0, fmt.Errorf("host %q is local, not remote", hostName)
	}

	hc, exists := m.hosts[hostName]
	if !exists {
		hc = NewHostConn(hostName, host, m.buildHash, m.onPaneOutput, m.onPaneExit, m.onStateChange)
		m.hosts[hostName] = hc
	}
	m.localToHost[localPaneID] = hostName
	m.mu.Unlock()

	// Connect if not already connected (thread-safe inside HostConn)
	if err := hc.EnsureConnected(sessionName); err != nil {
		return 0, fmt.Errorf("connecting to %s: %w", hostName, err)
	}

	// Create a pane on the remote server
	remotePaneID, err := hc.CreateRemotePane(localPaneID)
	if err != nil {
		return 0, fmt.Errorf("creating remote pane on %s: %w", hostName, err)
	}

	return remotePaneID, nil
}

// AttachForTakeover connects to a remote amux server that was started by a takeover
// and wires bidirectional I/O for all proxy panes. paneMappings maps local pane ID →
// remote pane ID. All mappings are registered before connecting so the readLoop
// never receives output for an unmapped pane.
func (m *Manager) AttachForTakeover(hostName, sshAddr, sshUser, remoteUID, sessionName string, paneMappings map[uint32]uint32) error {
	m.mu.Lock()

	// Find config entry by SSH address to inherit identity_file and user settings.
	connKey, hostCfg, found := m.findHostByAddress(sshAddr)
	if !found {
		connKey = hostName
		hostCfg = config.Host{Type: "remote", Address: sshAddr, User: sshUser}
	}

	hc, exists := m.hosts[connKey]
	if !exists {
		hc = NewHostConn(connKey, hostCfg, m.buildHash, m.onPaneOutput, m.onPaneExit, m.onStateChange)
		m.hosts[connKey] = hc
	}
	for localID := range paneMappings {
		m.localToHost[localID] = connKey
	}
	m.mu.Unlock()

	hc.BeginInputBuffering()

	// Register ALL pane mappings before connecting so readLoop routes output correctly
	// from the first message. If we connected first, early output from any pane not
	// yet registered would be silently dropped.
	for localID, remoteID := range paneMappings {
		hc.RegisterPane(localID, remoteID)
	}

	return hc.EnsureConnectedForTakeover(sessionName, remoteUID, sshAddr)
}

// findHostByAddress finds a config host entry whose Address matches sshAddr.
// Returns the host name, config entry, and true if found.
func (m *Manager) findHostByAddress(sshAddr string) (string, config.Host, bool) {
	sshAddr = normalizeAddr(sshAddr)
	for name, host := range m.cfg.Hosts {
		if host.Type == "local" {
			continue
		}
		addr := host.Address
		if addr == "" {
			addr = name
		}
		if normalizeAddr(addr) == sshAddr {
			return name, host, true
		}
	}
	return "", config.Host{}, false
}

// hostConnForPane returns the HostConn for a local pane, or nil if not found.
func (m *Manager) hostConnForPane(localPaneID uint32) *HostConn {
	m.mu.Lock()
	defer m.mu.Unlock()
	hostName, ok := m.localToHost[localPaneID]
	if !ok {
		return nil
	}
	return m.hosts[hostName]
}

// SendInput routes input from a local proxy pane to the correct remote host.
func (m *Manager) SendInput(localPaneID uint32, data []byte) error {
	hc := m.hostConnForPane(localPaneID)
	if hc == nil {
		return fmt.Errorf("no remote host for local pane %d", localPaneID)
	}
	return hc.SendInput(localPaneID, data)
}

// SendResize notifies the remote server about a pane resize.
func (m *Manager) SendResize(localPaneID uint32, cols, rows int) error {
	hc := m.hostConnForPane(localPaneID)
	if hc == nil {
		return nil // ignore resize for unknown panes
	}
	return hc.SendResize(localPaneID, cols, rows)
}

// KillPane forwards a kill request to the remote pane mapped to localPaneID.
func (m *Manager) KillPane(localPaneID uint32, cleanup bool, timeout time.Duration) error {
	hc := m.hostConnForPane(localPaneID)
	if hc == nil {
		return fmt.Errorf("no remote host for local pane %d", localPaneID)
	}
	return hc.KillPane(localPaneID, cleanup, timeout)
}

// RemovePane cleans up the local→remote mapping when a proxy pane is closed.
func (m *Manager) RemovePane(localPaneID uint32) {
	m.mu.Lock()
	hostName, ok := m.localToHost[localPaneID]
	if ok {
		delete(m.localToHost, localPaneID)
		if hc, exists := m.hosts[hostName]; exists {
			hc.RemovePane(localPaneID)
		}
	}
	m.mu.Unlock()
}

// HostStatus returns the connection state of a named host.
func (m *Manager) HostStatus(hostName string) ConnState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if hc, ok := m.hosts[hostName]; ok {
		return hc.State()
	}
	return Disconnected
}

// AllHostStatus returns connection states for all configured remote hosts.
func (m *Manager) AllHostStatus() map[string]ConnState {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make(map[string]ConnState)
	for name, host := range m.cfg.Hosts {
		if host.Type == "local" {
			continue
		}
		if hc, ok := m.hosts[name]; ok {
			result[name] = hc.State()
		} else {
			result[name] = Disconnected
		}
	}
	return result
}

// DisconnectHost drops the SSH connection to a host and marks panes as disconnected.
func (m *Manager) DisconnectHost(hostName string) error {
	m.mu.Lock()
	hc, ok := m.hosts[hostName]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("host %q not connected", hostName)
	}
	hc.Disconnect()
	return nil
}

// ReconnectHost manually triggers a reconnect attempt for a host.
func (m *Manager) ReconnectHost(hostName string, sessionName string) error {
	m.mu.Lock()
	hc, ok := m.hosts[hostName]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("host %q not known", hostName)
	}
	return hc.Reconnect(sessionName)
}

// ConnStatusForPane returns the connection status string for a local pane.
// Returns "" for local panes (not tracked by the manager).
func (m *Manager) ConnStatusForPane(localPaneID uint32) string {
	hc := m.hostConnForPane(localPaneID)
	if hc == nil {
		return ""
	}
	return string(hc.State())
}

// Shutdown disconnects all remote hosts and stops their event loops.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	hosts := make([]*HostConn, 0, len(m.hosts))
	for _, hc := range m.hosts {
		hosts = append(hosts, hc)
	}
	m.mu.Unlock()

	for _, hc := range hosts {
		hc.Close()
	}
}
