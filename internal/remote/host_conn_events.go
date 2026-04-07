package remote

import (
	"errors"
	"net"

	charmlog "github.com/charmbracelet/log"
	"golang.org/x/crypto/ssh"

	"github.com/weill-labs/amux/internal/proto"
)

var errHostConnClosed = errors.New("host connection closed")

// hostEvent is processed sequentially by the HostConn event loop.
// All mutable HostConn state is accessed only from within event handlers,
// eliminating the need for mutexes.
type hostEvent interface {
	handle(*HostConn)
}

// connectOutcome holds the results of a successful SSH+amux connect.
// Returned by doConnect/doConnectTakeover (which run outside the actor)
// and applied to HostConn state by the actor.
type connectOutcome struct {
	sshClient   *ssh.Client
	amuxConn    net.Conn
	amuxReader  *proto.Reader
	amuxWriter  *proto.Writer
	sessionName string
	remoteUID   string
	connectAddr string
	takeover    bool
}

// closeConns closes the connections held by a connectOutcome.
// Used when discarding an outcome that arrived after an explicit disconnect.
func (o *connectOutcome) closeConns() {
	if o.amuxConn != nil {
		o.amuxConn.Close()
	}
	if o.sshClient != nil {
		o.sshClient.Close()
	}
}

// --- Query events ---

type stateQuery struct {
	reply chan ConnState
}

func (e stateQuery) handle(hc *HostConn) {
	e.reply <- hc.state
}

type connInfoResult struct {
	sshClient   *ssh.Client
	sessionName string
	remoteUID   string
}

type connInfoQuery struct {
	reply chan connInfoResult
}

func (e connInfoQuery) handle(hc *HostConn) {
	e.reply <- connInfoResult{
		sshClient:   hc.sshClient,
		sessionName: hc.sessionName,
		remoteUID:   hc.remoteUID,
	}
}

type paneExistsQuery struct {
	localPaneID uint32
	reply       chan bool
}

func (e paneExistsQuery) handle(hc *HostConn) {
	_, ok := hc.localToRemote[e.localPaneID]
	e.reply <- ok
}

type remotePaneIDResult struct {
	remotePaneID uint32
	ok           bool
}

type remotePaneIDQuery struct {
	localPaneID uint32
	reply       chan remotePaneIDResult
}

func (e remotePaneIDQuery) handle(hc *HostConn) {
	remotePaneID, ok := hc.localToRemote[e.localPaneID]
	e.reply <- remotePaneIDResult{remotePaneID: remotePaneID, ok: ok}
}

// --- Connect events ---

type connectEvent struct {
	sessionName string
	reply       chan error
}

func (e connectEvent) handle(hc *HostConn) {
	hc.startConnect(e.reply, func() (*connectOutcome, error) {
		return hc.doConnect(e.sessionName)
	})
}

type connectTakeoverEvent struct {
	sessionName string
	remoteUID   string
	sshAddr     string
	reply       chan error
}

func (e connectTakeoverEvent) handle(hc *HostConn) {
	hc.startConnect(e.reply, func() (*connectOutcome, error) {
		return hc.doConnectTakeover(e.sessionName, e.remoteUID, e.sshAddr)
	})
}

// startConnect is the shared handler for connectEvent and connectTakeoverEvent.
// If already connected, replies immediately. If a connect is in progress, queues
// the reply. Otherwise transitions to Connecting and spawns connectFn.
func (hc *HostConn) startConnect(reply chan error, connectFn func() (*connectOutcome, error)) {
	if hc.state == Connected {
		reply <- nil
		return
	}

	hc.pendingConnectReplies = append(hc.pendingConnectReplies, reply)

	if hc.state == Connecting {
		return // connect already in progress, reply will come via connectDoneEvent
	}

	hc.setState(Connecting)
	go func() {
		outcome, err := connectFn()
		hc.enqueue(connectDoneEvent{outcome: outcome, err: err})
	}()
}

type connectDoneEvent struct {
	outcome *connectOutcome
	err     error
}

func (e connectDoneEvent) handle(hc *HostConn) {
	if e.err != nil {
		if hc.state == Connecting {
			hc.setState(Disconnected)
		}
		hc.bufferPendingInputs = false
		hc.pendingInputs = nil
		hc.drainPendingReplies(e.err)
		return
	}

	if hc.state != Connecting {
		// Explicit disconnect arrived while connecting -- discard the result.
		e.outcome.closeConns()
		hc.bufferPendingInputs = false
		hc.pendingInputs = nil
		hc.drainPendingReplies(errHostConnClosed)
		return
	}

	hc.applyOutcome(e.outcome)
	hc.drainPendingReplies(nil)
}

// --- Disconnect / Reconnect events ---

type disconnectEvent struct {
	reply chan struct{}
}

func (e disconnectEvent) handle(hc *HostConn) {
	if hc.state == Connected {
		hc.logSSHDisconnect(charmlog.InfoLevel, "explicit disconnect")
	}
	hc.disconnectAndDrainPending()
	if e.reply != nil {
		close(e.reply)
	}
}

type reconnectCmd struct {
	sessionName string
	reply       chan error
}

func (e reconnectCmd) handle(hc *HostConn) {
	if hc.state == Connected {
		hc.logSSHDisconnect(charmlog.InfoLevel, "reconnect")
	}
	hc.disconnectAndDrainPending()

	// Start a new connect.
	hc.startConnect(e.reply, func() (*connectOutcome, error) {
		return hc.doConnectWithAddr(e.sessionName, hc.connectAddr)
	})
}

// --- Pane mapping events ---

type registerPaneEvent struct {
	localPaneID  uint32
	remotePaneID uint32
}

type beginInputBufferingEvent struct{}

func (e beginInputBufferingEvent) handle(hc *HostConn) {
	hc.bufferPendingInputs = true
}

func (e registerPaneEvent) handle(hc *HostConn) {
	hc.localToRemote[e.localPaneID] = e.remotePaneID
	hc.remoteToLocal[e.remotePaneID] = e.localPaneID
}

type removePaneEvent struct {
	localPaneID uint32
}

func (e removePaneEvent) handle(hc *HostConn) {
	if remoteID, ok := hc.localToRemote[e.localPaneID]; ok {
		delete(hc.localToRemote, e.localPaneID)
		delete(hc.remoteToLocal, remoteID)
	}
}

// --- I/O events ---

type sendInputEvent struct {
	localPaneID uint32
	data        []byte
}

func (e sendInputEvent) handle(hc *HostConn) {
	if _, ok := hc.localToRemote[e.localPaneID]; !ok {
		return
	}
	if hc.amuxConn == nil {
		if hc.bufferPendingInputs || hc.state == Connecting || hc.state == Reconnecting {
			hc.pendingInputs = append(hc.pendingInputs, pendingPaneInput{
				localPaneID: e.localPaneID,
				data:        append([]byte(nil), e.data...),
			})
		}
		return
	}
	hc.sendInputNow(e.localPaneID, e.data)
}

type readPaneOutputEvent struct {
	remotePaneID uint32
	data         []byte
}

func (e readPaneOutputEvent) handle(hc *HostConn) {
	localID, ok := hc.remoteToLocal[e.remotePaneID]
	if ok && hc.onPaneOutput != nil {
		hc.onPaneOutput(localID, e.data)
	}
}

type readLayoutEvent struct {
	layout *proto.LayoutSnapshot
}

func (e readLayoutEvent) handle(hc *HostConn) {
	if e.layout == nil {
		return
	}
	if hc.takeoverMode && !layoutReady(e.layout) {
		return
	}

	present := make(map[uint32]struct{}, len(e.layout.Panes))
	for _, pane := range e.layout.Panes {
		present[pane.ID] = struct{}{}
	}
	if len(present) == 0 {
		for _, win := range e.layout.Windows {
			for _, pane := range win.Panes {
				present[pane.ID] = struct{}{}
			}
		}
	}

	var exited []uint32
	for remotePaneID, localPaneID := range hc.remoteToLocal {
		if _, ok := present[remotePaneID]; ok {
			continue
		}
		delete(hc.remoteToLocal, remotePaneID)
		delete(hc.localToRemote, localPaneID)
		exited = append(exited, localPaneID)
	}

	if hc.onPaneExit == nil {
		return
	}
	for _, localPaneID := range exited {
		hc.onPaneExit(localPaneID, "remote disconnect")
	}
}

type readDisconnectEvent struct{}

func (e readDisconnectEvent) handle(hc *HostConn) {
	if hc.state != Connected {
		return // already disconnected or reconnecting
	}
	hc.logSSHDisconnect(charmlog.WarnLevel, "remote disconnect")
	hc.setState(Reconnecting)
	hc.closeConns()

	// Capture reconnect parameters before spawning the goroutine.
	go hc.reconnectLoop(hc.reconnectTarget())
}

// reconnectDoneEvent is sent by the reconnect goroutine on success.
type reconnectDoneEvent struct {
	outcome *connectOutcome
	done    chan struct{} // closed when applied
}

func (e reconnectDoneEvent) handle(hc *HostConn) {
	defer close(e.done)
	if hc.state != Reconnecting {
		// Explicit disconnect while reconnecting -- discard.
		e.outcome.closeConns()
		return
	}

	hc.applyOutcome(e.outcome)
}

// --- Actor helpers (called only from the event loop goroutine) ---

// applyOutcome installs a successful connect result into the HostConn,
// transitions to Connected, and starts the read loop. Shared by
// connectDoneEvent and reconnectDoneEvent.
func (hc *HostConn) applyOutcome(o *connectOutcome) {
	hc.sshClient = o.sshClient
	hc.amuxConn = o.amuxConn
	hc.amuxReader = o.amuxReader
	hc.amuxWriter = o.amuxWriter
	hc.sessionName = o.sessionName
	hc.remoteUID = o.remoteUID
	hc.connectAddr = o.connectAddr
	if o.takeover {
		hc.takeoverMode = true
	}
	hc.setState(Connected)
	hc.logSSHConnect()
	hc.bufferPendingInputs = false
	hc.flushPendingInputs()
	go hc.readLoop(hc.amuxReader)
}

// drainPendingReplies sends err to all pending connect waiters and clears the slice.
func (hc *HostConn) drainPendingReplies(err error) {
	for _, reply := range hc.pendingConnectReplies {
		reply <- err
	}
	hc.pendingConnectReplies = nil
}

// disconnectAndDrainPending closes connections, transitions to Disconnected,
// and cancels all pending connect waiters. Shared by disconnectEvent and
// reconnectCmd.
func (hc *HostConn) disconnectAndDrainPending() {
	hc.closeConns()
	hc.setState(Disconnected)
	hc.bufferPendingInputs = false
	hc.pendingInputs = nil
	hc.drainPendingReplies(errHostConnClosed)
}

type reconnectTarget struct {
	sessionName string
	remoteUID   string
	connectAddr string
	takeover    bool
}

func (hc *HostConn) reconnectTarget() reconnectTarget {
	connectAddr := hc.connectAddr
	if connectAddr == "" {
		connectAddr = normalizedDialAddr(hc.name, hc.config.Address)
	}
	return reconnectTarget{
		sessionName: hc.sessionName,
		remoteUID:   hc.remoteUID,
		connectAddr: connectAddr,
		takeover:    hc.takeoverMode,
	}
}

// --- Event loop ---

func (hc *HostConn) startEventLoop() {
	hc.cmds = make(chan hostEvent, 256)
	hc.stop = make(chan struct{})
	hc.done = make(chan struct{})
	go hc.eventLoop()
}

func (hc *HostConn) eventLoop() {
	defer close(hc.done)
	for {
		select {
		case <-hc.stop:
			return
		case ev := <-hc.cmds:
			if ev != nil {
				ev.handle(hc)
			}
		}
	}
}

func (hc *HostConn) enqueue(ev hostEvent) bool {
	select {
	case <-hc.stop:
		return false
	default:
	}

	select {
	case <-hc.stop:
		return false
	case hc.cmds <- ev:
		return true
	}
}

// Close stops the event loop and releases resources.
// Safe to call multiple times; only the first call has effect.
func (hc *HostConn) Close() {
	hc.closeOnce.Do(func() {
		hc.Disconnect()
		close(hc.stop)
		<-hc.done
	})
}
