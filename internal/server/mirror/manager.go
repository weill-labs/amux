package mirror

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/remote"
)

type State string

const (
	StateConnecting   State = "connecting"
	StateConnected    State = "connected"
	StateReconnecting State = "reconnecting"
	StateDead         State = "dead"
	StateDetached     State = "detached"
)

const (
	defaultAttachTimeout = 10 * time.Second
)

var errRemotePaneExited = errors.New("remote pane exited")

type Config struct {
	Hosts         map[string]config.Host
	Dialer        remote.Dialer
	RetryPolicy   remote.RetryPolicy
	AttachTimeout time.Duration
}

type Snapshot struct {
	State        State
	Generation   uint64
	RemotePaneID uint32
	RemoteRef    checkpoint.RemoteRef
	LastError    string
}

type Manager struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu            sync.Mutex
	hosts         map[string]config.Host
	dialer        remote.Dialer
	retryPolicy   remote.RetryPolicy
	attachTimeout time.Duration
	mirrors       map[uint32]*mirrorState
	wg            sync.WaitGroup
}

type mirrorState struct {
	pane          *mux.Pane
	ref           checkpoint.RemoteRef
	state         State
	generation    uint64
	remotePaneID  uint32
	link          *remote.Link
	running       bool
	bootstrapping bool
	history       []string
	lastErr       string
}

func NewManager(cfg Config) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		ctx:     ctx,
		cancel:  cancel,
		mirrors: make(map[uint32]*mirrorState),
	}
	m.Configure(cfg)
	return m
}

func (m *Manager) Configure(cfg Config) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.hosts = cloneHosts(cfg.Hosts)
	m.dialer = cfg.Dialer
	m.retryPolicy = normalizeRetryPolicy(cfg.RetryPolicy)
	m.attachTimeout = cfg.AttachTimeout
	if m.attachTimeout <= 0 {
		m.attachTimeout = defaultAttachTimeout
	}
	var start []*mirrorState
	for _, ms := range m.mirrors {
		if ms.running || ms.state == StateDetached || ms.state == StateDead {
			continue
		}
		if _, ok := m.hostForRefLocked(ms.ref); ok {
			start = append(start, ms)
		}
	}
	for _, ms := range start {
		m.startLocked(ms)
	}
	m.mu.Unlock()
}

func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.cancel()

	m.mu.Lock()
	links := make([]*remote.Link, 0, len(m.mirrors))
	for _, ms := range m.mirrors {
		if ms.link != nil {
			links = append(links, ms.link)
		}
	}
	m.mu.Unlock()

	for _, link := range links {
		_ = link.Close()
	}
	m.wg.Wait()
}

func (m *Manager) Track(pane *mux.Pane, ref checkpoint.RemoteRef) error {
	if m == nil {
		return fmt.Errorf("mirror manager is nil")
	}
	if pane == nil {
		return fmt.Errorf("mirror pane is nil")
	}
	if ref.Host == "" {
		return fmt.Errorf("remote host is required")
	}
	if ref.PaneName == "" {
		return fmt.Errorf("remote pane name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	ms := &mirrorState{
		pane:  pane,
		ref:   ref,
		state: StateConnecting,
	}
	m.mirrors[pane.ID] = ms
	if _, ok := m.hostForRefLocked(ref); ok {
		m.startLocked(ms)
	} else {
		ms.lastErr = fmt.Sprintf("remote host %q is not configured", ref.Host)
	}
	return nil
}

func (m *Manager) Detach(paneID uint32) {
	if m == nil || paneID == 0 {
		return
	}
	m.mu.Lock()
	ms := m.mirrors[paneID]
	if ms == nil {
		m.mu.Unlock()
		return
	}
	ms.state = StateDetached
	ms.pane = nil
	link := ms.link
	ms.link = nil
	m.mu.Unlock()
	if link != nil {
		_ = link.Close()
	}
}

func (m *Manager) Write(paneID uint32, data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if m == nil {
		return len(data), nil
	}

	m.mu.Lock()
	ms := m.mirrors[paneID]
	if ms == nil || ms.state != StateConnected || ms.link == nil || ms.remotePaneID == 0 {
		m.mu.Unlock()
		return len(data), nil
	}
	link := ms.link
	remotePaneID := ms.remotePaneID
	m.mu.Unlock()

	if err := link.WriteMsg(&proto.Message{
		Type:     proto.MsgTypeInputPane,
		PaneID:   remotePaneID,
		PaneData: append([]byte(nil), data...),
	}); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (m *Manager) RemoteRef(paneID uint32) (*checkpoint.RemoteRef, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ms := m.mirrors[paneID]
	if ms == nil {
		return nil, false
	}
	ref := ms.ref
	return &ref, true
}

func (m *Manager) Snapshot(paneID uint32) (Snapshot, bool) {
	if m == nil {
		return Snapshot{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ms := m.mirrors[paneID]
	if ms == nil {
		return Snapshot{}, false
	}
	return Snapshot{
		State:        ms.state,
		Generation:   ms.generation,
		RemotePaneID: ms.remotePaneID,
		RemoteRef:    ms.ref,
		LastError:    ms.lastErr,
	}, true
}

func (m *Manager) startLocked(ms *mirrorState) {
	if ms == nil || ms.running || ms.state == StateDetached || ms.state == StateDead {
		return
	}
	ms.running = true
	paneID := ms.pane.ID
	m.wg.Add(1)
	go m.run(paneID, ms)
}

func (m *Manager) run(paneID uint32, owner *mirrorState) {
	defer m.wg.Done()
	defer m.markStopped(paneID, owner)

	attempt := 0
	first := true
	for {
		if err := m.ctx.Err(); err != nil {
			return
		}
		if !m.isCurrent(paneID, owner) {
			return
		}
		state := StateConnecting
		if !first {
			attempt++
			if attempt > m.retryPolicy.MaxAttempts {
				m.markDeadForOwner(paneID, owner, "remote connection retry budget exhausted")
				return
			}
			state = StateReconnecting
			if !m.sleepBeforeRetry(attempt) {
				return
			}
			if !m.isCurrent(paneID, owner) {
				return
			}
		}

		connected, terminal := m.attachAndRead(paneID, owner, state)
		if terminal {
			return
		}
		first = false
		if connected {
			attempt = 0
		}
	}
}

func (m *Manager) markStopped(paneID uint32, owner *mirrorState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ms := m.mirrors[paneID]; ms != nil && ms == owner {
		ms.running = false
	}
}

func (m *Manager) isCurrent(paneID uint32, owner *mirrorState) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mirrors[paneID] == owner
}

func (m *Manager) sleepBeforeRetry(attempt int) bool {
	delay := m.retryPolicy.Delay(attempt)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-m.ctx.Done():
		return false
	}
}

func (m *Manager) attachAndRead(paneID uint32, owner *mirrorState, state State) (connected bool, terminal bool) {
	host, ref, ok := m.prepareAttempt(paneID, owner, state)
	if !ok {
		return false, true
	}

	remotePaneID, err := m.resolvePaneID(host, ref)
	if err != nil {
		m.recordAttemptErrorForOwner(paneID, owner, err)
		return false, false
	}

	attachCtx, cancel := context.WithTimeout(m.ctx, m.attachTimeout)
	defer cancel()

	link := remote.NewLink(host, m.currentDialer())
	if err := link.Connect(attachCtx); err != nil {
		m.recordAttemptErrorForOwner(paneID, owner, err)
		return false, false
	}
	if err := link.WriteMsg(&proto.Message{
		Type:    proto.MsgTypeAttachPane,
		Session: remoteSession(host, ref),
		PaneID:  remotePaneID,
	}); err != nil {
		_ = link.Close()
		m.recordAttemptErrorForOwner(paneID, owner, err)
		return false, false
	}

	generation, ok := m.markConnectedForOwner(paneID, owner, remotePaneID, link)
	if !ok {
		_ = link.Close()
		return true, true
	}

	err = m.readLoop(paneID, owner, generation, link)
	_ = link.Close()
	if errors.Is(err, errRemotePaneExited) {
		m.markDeadForOwner(paneID, owner, "remote pane exited")
		return true, true
	}
	if err != nil {
		m.recordAttemptErrorForOwner(paneID, owner, err)
		if m.isTerminal(paneID, owner) {
			return true, true
		}
	}
	return true, false
}

func (m *Manager) prepareAttempt(paneID uint32, owner *mirrorState, state State) (config.Host, checkpoint.RemoteRef, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ms := m.mirrors[paneID]
	if ms == nil || (owner != nil && ms != owner) || ms.state == StateDetached || ms.state == StateDead {
		return config.Host{}, checkpoint.RemoteRef{}, false
	}
	ms.state = state
	host, ok := m.hostForRefLocked(ms.ref)
	if !ok {
		ms.lastErr = fmt.Sprintf("remote host %q is not configured", ms.ref.Host)
		return config.Host{}, ms.ref, false
	}
	return host, ms.ref, true
}

func (m *Manager) resolvePaneID(host config.Host, ref checkpoint.RemoteRef) (uint32, error) {
	ctx, cancel := context.WithTimeout(m.ctx, m.attachTimeout)
	defer cancel()

	conn, err := m.currentDialer().Dial(ctx, host)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	return remote.ResolvePaneID(ctx, conn, remoteSession(host, ref), ref.PaneName)
}

func (m *Manager) currentDialer() remote.Dialer {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dialer != nil {
		return m.dialer
	}
	return remote.SSHDialer{}
}

func (m *Manager) markConnected(paneID, remotePaneID uint32, link *remote.Link) (uint64, bool) {
	return m.markConnectedForOwner(paneID, nil, remotePaneID, link)
}

func (m *Manager) markConnectedForOwner(paneID uint32, owner *mirrorState, remotePaneID uint32, link *remote.Link) (uint64, bool) {
	var (
		pane    *mux.Pane
		oldLink *remote.Link
		gen     uint64
	)
	m.mu.Lock()
	ms := m.mirrors[paneID]
	if ms == nil || (owner != nil && ms != owner) || ms.state == StateDetached || ms.state == StateDead {
		m.mu.Unlock()
		return 0, false
	}
	oldLink = ms.link
	ms.generation++
	gen = ms.generation
	ms.remotePaneID = remotePaneID
	ms.link = link
	ms.state = StateConnected
	ms.lastErr = ""
	ms.history = nil
	ms.bootstrapping = true
	pane = ms.pane
	m.mu.Unlock()

	if oldLink != nil && oldLink != link {
		_ = oldLink.Close()
	}
	if pane != nil {
		pane.ResetState()
	}
	return gen, true
}

func (m *Manager) readLoop(paneID uint32, owner *mirrorState, generation uint64, link *remote.Link) error {
	for {
		msg, err := link.ReadMsg()
		if err != nil {
			return err
		}
		if err := m.applyMessageForOwner(paneID, owner, generation, msg); err != nil {
			return err
		}
	}
}

func (m *Manager) applyMessage(paneID uint32, generation uint64, msg *proto.Message) error {
	return m.applyMessageForOwner(paneID, nil, generation, msg)
}

func (m *Manager) applyMessageForOwner(paneID uint32, owner *mirrorState, generation uint64, msg *proto.Message) error {
	if msg == nil {
		return nil
	}
	var (
		pane         *mux.Pane
		data         []byte
		history      []string
		applyHistory bool
	)
	m.mu.Lock()
	ms := m.mirrors[paneID]
	if ms == nil || (owner != nil && ms != owner) || generation != ms.generation {
		m.mu.Unlock()
		return nil
	}
	switch msg.Type {
	case proto.MsgTypePaneHistory:
		if ms.bootstrapping && ms.history != nil {
			ms.history = append(ms.history, msg.History...)
		} else {
			ms.history = append([]string(nil), msg.History...)
		}
		history = append([]string(nil), ms.history...)
		applyHistory = true
		pane = ms.pane
	case proto.MsgTypePaneOutput:
		ms.bootstrapping = false
		data = append([]byte(nil), msg.PaneData...)
		pane = ms.pane
	case proto.MsgTypeExit:
		ms.state = StateDead
		ms.lastErr = "remote pane exited"
		ms.link = nil
		pane = ms.pane
	case proto.MsgTypeCmdResult:
		if msg.CmdErr != "" {
			ms.state = StateDead
			ms.lastErr = msg.CmdErr
			ms.link = nil
			pane = ms.pane
		}
	default:
	}
	m.mu.Unlock()

	if applyHistory && pane != nil {
		pane.SetRetainedHistory(history)
	}
	if data != nil && pane != nil {
		pane.FeedOutput(data)
	}
	if msg.Type == proto.MsgTypeExit {
		if pane != nil {
			pane.FeedOutput([]byte("\r\n[remote pane exited]\r\n"))
		}
		return errRemotePaneExited
	}
	if msg.Type == proto.MsgTypeCmdResult && msg.CmdErr != "" {
		if pane != nil {
			pane.FeedOutput([]byte("\r\n[" + msg.CmdErr + "]\r\n"))
		}
		return fmt.Errorf("%s", msg.CmdErr)
	}
	return nil
}

func (m *Manager) markDead(paneID uint32, message string) {
	m.markDeadForOwner(paneID, nil, message)
}

func (m *Manager) markDeadForOwner(paneID uint32, owner *mirrorState, message string) {
	var pane *mux.Pane
	m.mu.Lock()
	ms := m.mirrors[paneID]
	if ms == nil || (owner != nil && ms != owner) || ms.state == StateDetached || ms.state == StateDead {
		m.mu.Unlock()
		return
	}
	ms.state = StateDead
	ms.lastErr = message
	link := ms.link
	ms.link = nil
	pane = ms.pane
	m.mu.Unlock()
	if link != nil {
		_ = link.Close()
	}
	if pane != nil && message != "" {
		pane.FeedOutput([]byte("\r\n[" + message + "]\r\n"))
	}
}

func (m *Manager) isTerminal(paneID uint32, owner *mirrorState) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	ms := m.mirrors[paneID]
	return ms == nil || (owner != nil && ms != owner) || ms.state == StateDetached || ms.state == StateDead
}

func (m *Manager) recordAttemptError(paneID uint32, err error) {
	m.recordAttemptErrorForOwner(paneID, nil, err)
}

func (m *Manager) recordAttemptErrorForOwner(paneID uint32, owner *mirrorState, err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if ms := m.mirrors[paneID]; ms != nil && (owner == nil || ms == owner) && ms.state != StateDetached && ms.state != StateDead {
		ms.lastErr = err.Error()
	}
}

func (m *Manager) hostForRefLocked(ref checkpoint.RemoteRef) (config.Host, bool) {
	if m.hosts == nil {
		return config.Host{}, false
	}
	host, ok := m.hosts[ref.Host]
	if !ok {
		return config.Host{}, false
	}
	if host.Session == "" {
		host.Session = ref.Session
	}
	return host, true
}

func cloneHosts(src map[string]config.Host) map[string]config.Host {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]config.Host, len(src))
	for name, host := range src {
		dst[name] = host
	}
	return dst
}

func normalizeRetryPolicy(policy remote.RetryPolicy) remote.RetryPolicy {
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = remote.DefaultRetryPolicy().MaxAttempts
	}
	if policy.InitialBackoff <= 0 {
		policy.InitialBackoff = remote.DefaultRetryPolicy().InitialBackoff
	}
	if policy.MaxBackoff <= 0 {
		policy.MaxBackoff = remote.DefaultRetryPolicy().MaxBackoff
	}
	return policy
}

func remoteSession(host config.Host, ref checkpoint.RemoteRef) string {
	if ref.Session != "" {
		return ref.Session
	}
	return host.Session
}
