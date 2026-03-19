package remote

import (
	"errors"
	"net"

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
	sessionName string
	remoteUID   string
	takeover    bool
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

// --- Connect events ---

type connectEvent struct {
	sessionName string
	reply       chan error
}

func (e connectEvent) handle(hc *HostConn) {
	if hc.state == Connected {
		e.reply <- nil
		return
	}

	hc.pendingConnectReplies = append(hc.pendingConnectReplies, e.reply)

	if hc.state == Connecting {
		return // connect already in progress, reply will come via connectDoneEvent
	}

	hc.setState(Connecting)
	sessionName := e.sessionName
	go func() {
		outcome, err := hc.doConnect(sessionName)
		hc.enqueue(connectDoneEvent{outcome: outcome, err: err})
	}()
}

type connectTakeoverEvent struct {
	sessionName string
	remoteUID   string
	sshAddr     string
	reply       chan error
}

func (e connectTakeoverEvent) handle(hc *HostConn) {
	if hc.state == Connected {
		e.reply <- nil
		return
	}

	hc.pendingConnectReplies = append(hc.pendingConnectReplies, e.reply)

	if hc.state == Connecting {
		return
	}

	hc.setState(Connecting)
	go func() {
		outcome, err := hc.doConnectTakeover(e.sessionName, e.remoteUID, e.sshAddr)
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
		for _, reply := range hc.pendingConnectReplies {
			reply <- e.err
		}
		hc.pendingConnectReplies = nil
		return
	}

	if hc.state != Connecting {
		// Explicit disconnect arrived while connecting — discard the result.
		e.outcome.amuxConn.Close()
		e.outcome.sshClient.Close()
		for _, reply := range hc.pendingConnectReplies {
			reply <- errHostConnClosed
		}
		hc.pendingConnectReplies = nil
		return
	}

	hc.sshClient = e.outcome.sshClient
	hc.amuxConn = e.outcome.amuxConn
	hc.sessionName = e.outcome.sessionName
	hc.remoteUID = e.outcome.remoteUID
	if e.outcome.takeover {
		hc.takeoverMode = true
	}
	hc.setState(Connected)
	go hc.readLoop(hc.amuxConn)

	for _, reply := range hc.pendingConnectReplies {
		reply <- nil
	}
	hc.pendingConnectReplies = nil
}

// --- Disconnect / Reconnect events ---

type disconnectEvent struct {
	reply chan struct{}
}

func (e disconnectEvent) handle(hc *HostConn) {
	hc.closeConns()
	hc.setState(Disconnected)

	// Cancel any in-flight connect waiters.
	for _, reply := range hc.pendingConnectReplies {
		reply <- errHostConnClosed
	}
	hc.pendingConnectReplies = nil

	if e.reply != nil {
		close(e.reply)
	}
}

type reconnectCmd struct {
	sessionName string
	reply       chan error
}

func (e reconnectCmd) handle(hc *HostConn) {
	// Disconnect synchronously within the actor.
	hc.closeConns()
	hc.setState(Disconnected)
	for _, reply := range hc.pendingConnectReplies {
		reply <- errHostConnClosed
	}
	hc.pendingConnectReplies = nil

	// Start a new connect.
	hc.pendingConnectReplies = append(hc.pendingConnectReplies, e.reply)
	hc.setState(Connecting)
	sessionName := e.sessionName
	go func() {
		outcome, err := hc.doConnect(sessionName)
		hc.enqueue(connectDoneEvent{outcome: outcome, err: err})
	}()
}

// --- Pane mapping events ---

type registerPaneEvent struct {
	localPaneID  uint32
	remotePaneID uint32
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
	remotePaneID, ok := hc.localToRemote[e.localPaneID]
	if !ok || hc.amuxConn == nil {
		return
	}
	// Actor serializes all writes — replaces the old writeMu.
	proto.WriteMsg(hc.amuxConn, &proto.Message{
		Type:     proto.MsgTypeInputPane,
		PaneID:   remotePaneID,
		PaneData: e.data,
	})
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

type readDisconnectEvent struct{}

func (e readDisconnectEvent) handle(hc *HostConn) {
	if hc.state != Connected {
		return // already disconnected or reconnecting
	}
	hc.setState(Reconnecting)
	hc.closeConns()

	// Capture reconnect parameters before spawning the goroutine.
	sessionName := hc.sessionName
	remoteUID := hc.remoteUID
	isTakeover := hc.takeoverMode
	sshAddr := normalizeAddr(hc.config.Address)
	go hc.reconnectLoop(sessionName, remoteUID, isTakeover, sshAddr)
}

// reconnectDoneEvent is sent by the reconnect goroutine on success.
type reconnectDoneEvent struct {
	outcome *connectOutcome
	done    chan struct{} // closed when applied
}

func (e reconnectDoneEvent) handle(hc *HostConn) {
	defer close(e.done)
	if hc.state != Reconnecting {
		// Explicit disconnect while reconnecting — discard.
		if e.outcome.amuxConn != nil {
			e.outcome.amuxConn.Close()
		}
		if e.outcome.sshClient != nil {
			e.outcome.sshClient.Close()
		}
		return
	}

	hc.sshClient = e.outcome.sshClient
	hc.amuxConn = e.outcome.amuxConn
	hc.sessionName = e.outcome.sessionName
	hc.remoteUID = e.outcome.remoteUID
	if e.outcome.takeover {
		hc.takeoverMode = true
	}
	hc.setState(Connected)
	go hc.readLoop(hc.amuxConn)
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
// Must be called when the HostConn is no longer needed.
func (hc *HostConn) Close() {
	hc.Disconnect()
	close(hc.stop)
	<-hc.done
}
