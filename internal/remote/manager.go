// Package remote manages SSH connections to remote amux servers and proxies
// pane I/O between the local and remote instances. The local amux server acts
// as a client to each remote amux server, reusing the existing wire protocol.
package remote

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	charmlog "github.com/charmbracelet/log"
	"golang.org/x/crypto/ssh"

	"github.com/weill-labs/amux/internal/auditlog"
	"github.com/weill-labs/amux/internal/config"
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

// HostConnFactory constructs HostConn instances for both managed and temporary
// deploy-only connections.
type HostConnFactory func(
	name string,
	cfg config.Host,
	buildHash string,
	onOutput PaneOutputCallback,
	onExit PaneExitCallback,
	onStateChange StateChangeCallback,
) *HostConn

// ManagerDeps holds explicit constructor dependencies for Manager.
type ManagerDeps struct {
	OnPaneOutput  PaneOutputCallback
	OnPaneExit    PaneExitCallback
	OnStateChange StateChangeCallback
	NewHostConn   HostConnFactory
}

// Manager coordinates all remote host connections. It maps local pane IDs
// to their remote counterparts and routes I/O through the appropriate HostConn.
//
// Concurrency:
// All mutable manager state is owned by a single event-loop goroutine. Public
// methods enqueue events to that actor and may wait for replies. Dependencies
// are fixed at construction, and callers must treat the Config pointer returned
// by Config as read-only.
type Manager struct {
	// Immutable after construction.
	cfg         *config.Config
	buildHash   string
	newHostConn HostConnFactory
	logger      *charmlog.Logger

	onPaneOutput  PaneOutputCallback
	onPaneExit    PaneExitCallback
	onStateChange StateChangeCallback

	// Actor-owned state.
	hosts       map[string]*HostConn // keyed by config host name
	localToHost map[uint32]string    // local pane ID -> host name

	// Event loop lifecycle.
	cmds      chan managerEvent
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	closed    atomic.Bool
}

// NewManager creates a remote host manager with the given config and explicit
// constructor dependencies.
func NewManager(cfg *config.Config, buildHash string, deps ManagerDeps) *Manager {
	if deps.NewHostConn == nil {
		panic("remote.NewManager: NewHostConn is required")
	}

	m := &Manager{
		cfg:           cfg,
		buildHash:     buildHash,
		newHostConn:   deps.NewHostConn,
		logger:        auditlog.Discard(),
		onPaneOutput:  deps.OnPaneOutput,
		onPaneExit:    deps.OnPaneExit,
		onStateChange: deps.OnStateChange,
		hosts:         make(map[string]*HostConn),
		localToHost:   make(map[uint32]string),
	}
	m.startEventLoop()
	return m
}

// Config returns the underlying config.
func (m *Manager) Config() *config.Config {
	return m.cfg
}

func (m *Manager) newManagedHostConn(name string, cfg config.Host) *HostConn {
	return m.newHostConn(name, cfg, m.buildHash, m.onPaneOutput, m.onPaneExit, m.onStateChange)
}

func (m *Manager) newDeployHostConn(name string, cfg config.Host) *HostConn {
	return m.newHostConn(name, cfg, m.buildHash, nil, nil, nil)
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

	hc := m.newDeployHostConn("deploy-tmp", hostCfg)
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

// CreatePane establishes or reuses an SSH connection to the named host and
// creates a new pane on the remote amux server. Returns the remote pane ID.
// The localPaneID is used for routing output back to the correct local pane.
func (m *Manager) CreatePane(hostName string, localPaneID uint32, sessionName string) (uint32, error) {
	hc, err := enqueueManagerQuery(m, func(m *Manager) (*HostConn, error) {
		host, ok := m.cfg.Hosts[hostName]
		if !ok {
			return nil, fmt.Errorf("host %q not found in config", hostName)
		}
		if host.Type == "local" {
			return nil, fmt.Errorf("host %q is local, not remote", hostName)
		}

		hc, ok := m.hosts[hostName]
		if !ok {
			hc = m.newManagedHostConn(hostName, host)
			m.hosts[hostName] = hc
		}
		m.localToHost[localPaneID] = hostName
		return hc, nil
	})
	if err != nil {
		return 0, err
	}

	if err := hc.EnsureConnected(sessionName); err != nil {
		return 0, fmt.Errorf("connecting to %s: %w", hostName, err)
	}

	remotePaneID, err := hc.CreateRemotePane(localPaneID)
	if err != nil {
		return 0, fmt.Errorf("creating remote pane on %s: %w", hostName, err)
	}

	return remotePaneID, nil
}

// AttachForTakeover connects to a remote amux server that was started by a takeover
// and wires bidirectional I/O for all proxy panes. paneMappings maps local pane ID ->
// remote pane ID. All mappings are registered before connecting so the readLoop never
// receives output for an unmapped pane.
func (m *Manager) AttachForTakeover(hostName, sshAddr, sshUser, remoteUID, sessionName string, paneMappings map[uint32]uint32) error {
	hc, err := enqueueManagerQuery(m, func(m *Manager) (*HostConn, error) {
		connKey, hostCfg, found := m.findHostByAddress(sshAddr)
		if !found {
			connKey = hostName
			hostCfg = config.Host{Type: "remote", Address: sshAddr, User: sshUser}
		}

		hc, ok := m.hosts[connKey]
		if !ok {
			hc = m.newManagedHostConn(connKey, hostCfg)
			m.hosts[connKey] = hc
		}
		for localID := range paneMappings {
			m.localToHost[localID] = connKey
		}
		return hc, nil
	})
	if err != nil {
		return err
	}

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
	hc, err := enqueueManagerQuery(m, func(m *Manager) (*HostConn, error) {
		hostName, ok := m.localToHost[localPaneID]
		if !ok {
			return nil, nil
		}
		return m.hosts[hostName], nil
	})
	if err != nil {
		return nil
	}
	return hc
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

// RemovePane cleans up the local->remote mapping when a proxy pane is closed.
func (m *Manager) RemovePane(localPaneID uint32) {
	remove, err := enqueueManagerQuery(m, func(m *Manager) (*HostConn, error) {
		hostName, ok := m.localToHost[localPaneID]
		if !ok {
			return nil, nil
		}
		delete(m.localToHost, localPaneID)
		return m.hosts[hostName], nil
	})
	if err != nil || remove == nil {
		return
	}
	remove.RemovePane(localPaneID)
}

// HostStatus returns the connection state of a named host.
func (m *Manager) HostStatus(hostName string) ConnState {
	hc, err := enqueueManagerQuery(m, func(m *Manager) (*HostConn, error) {
		return m.hosts[hostName], nil
	})
	if err != nil || hc == nil {
		return Disconnected
	}
	return hc.State()
}

// AllHostStatus returns connection states for all configured remote hosts.
func (m *Manager) AllHostStatus() map[string]ConnState {
	hosts, err := enqueueManagerQuery(m, func(m *Manager) (map[string]*HostConn, error) {
		result := make(map[string]*HostConn)
		for name, host := range m.cfg.Hosts {
			if host.Type == "local" {
				continue
			}
			result[name] = m.hosts[name]
		}
		return result, nil
	})
	if err != nil {
		return map[string]ConnState{}
	}

	statuses := make(map[string]ConnState, len(hosts))
	for name, hc := range hosts {
		if hc == nil {
			statuses[name] = Disconnected
			continue
		}
		statuses[name] = hc.State()
	}
	return statuses
}

// DisconnectHost drops the SSH connection to a host and marks panes as disconnected.
func (m *Manager) DisconnectHost(hostName string) error {
	hc, err := enqueueManagerQuery(m, func(m *Manager) (*HostConn, error) {
		hc, ok := m.hosts[hostName]
		if !ok {
			return nil, fmt.Errorf("host %q not connected", hostName)
		}
		return hc, nil
	})
	if err != nil {
		return err
	}
	hc.Disconnect()
	return nil
}

// ReconnectHost manually triggers a reconnect attempt for a host.
func (m *Manager) ReconnectHost(hostName string, sessionName string) error {
	hc, err := enqueueManagerQuery(m, func(m *Manager) (*HostConn, error) {
		hc, ok := m.hosts[hostName]
		if !ok {
			return nil, fmt.Errorf("host %q not known", hostName)
		}
		return hc, nil
	})
	if err != nil {
		return err
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
	m.closeOnce.Do(func() {
		m.closed.Store(true)
		close(m.stop)

		reply := make(chan []*HostConn, 1)
		select {
		case <-m.done:
			return
		case m.cmds <- managerShutdownEvent{reply: reply}:
		}

		hosts := <-reply
		<-m.done

		for _, hc := range hosts {
			hc.Close()
		}
	})
}
