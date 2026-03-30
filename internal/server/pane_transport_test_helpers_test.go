package server

import (
	"fmt"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

type stubPaneTransport struct {
	createPaneErr    error
	createPaneRemote uint32
	createPaneCalls  []createPaneCall
	sendInputCalls   []sendInputCall
	sendInputErr     error
	sendResizeErr    error
	killPaneErr      error
	connStatusByPane map[uint32]string
	hostStatusByName map[string]proto.ConnState
	disconnectErrs   map[string]error
	reconnectErrs    map[string]error
	removedPanes     []uint32
	shutdownCalls    int
	attachErr        error
	attachCalls      []attachForTakeoverCall
	deployCalls      []deployCall
}

type createPaneCall struct {
	hostName    string
	localPaneID uint32
	sessionName string
}

type sendInputCall struct {
	localPaneID uint32
	data        []byte
}

type attachForTakeoverCall struct {
	hostName    string
	sshAddr     string
	sshUser     string
	remoteUID   string
	sessionName string
	paneMap     map[uint32]uint32
}

type deployCall struct {
	hostName string
	sshAddr  string
	sshUser  string
}

type stubTakeoverOnlyTransport struct {
	attachErr   error
	attachCalls []attachForTakeoverCall
	deployCalls []deployCall
}

func (s *stubPaneTransport) SendInput(localPaneID uint32, data []byte) error {
	s.sendInputCalls = append(s.sendInputCalls, sendInputCall{
		localPaneID: localPaneID,
		data:        append([]byte(nil), data...),
	})
	return s.sendInputErr
}

func (s *stubPaneTransport) SendResize(uint32, int, int) error {
	return s.sendResizeErr
}

func (s *stubPaneTransport) KillPane(uint32, bool, time.Duration) error {
	return s.killPaneErr
}

func (s *stubPaneTransport) RemovePane(localPaneID uint32) {
	s.removedPanes = append(s.removedPanes, localPaneID)
}

func (s *stubPaneTransport) CreatePane(hostName string, localPaneID uint32, sessionName string) (uint32, error) {
	s.createPaneCalls = append(s.createPaneCalls, createPaneCall{
		hostName:    hostName,
		localPaneID: localPaneID,
		sessionName: sessionName,
	})
	if s.createPaneErr != nil {
		return 0, s.createPaneErr
	}
	if s.createPaneRemote != 0 {
		return s.createPaneRemote, nil
	}
	return 1, nil
}

func (s *stubPaneTransport) ConnStatusForPane(localPaneID uint32) string {
	if s.connStatusByPane == nil {
		return ""
	}
	return s.connStatusByPane[localPaneID]
}

func (s *stubPaneTransport) HostStatus(hostName string) proto.ConnState {
	if s.hostStatusByName == nil {
		return proto.Disconnected
	}
	if status, ok := s.hostStatusByName[hostName]; ok {
		return status
	}
	return proto.Disconnected
}

func (s *stubPaneTransport) AllHostStatus() map[string]proto.ConnState {
	if s.hostStatusByName == nil {
		return map[string]proto.ConnState{}
	}
	out := make(map[string]proto.ConnState, len(s.hostStatusByName))
	for hostName, status := range s.hostStatusByName {
		out[hostName] = status
	}
	return out
}

func (s *stubPaneTransport) DisconnectHost(hostName string) error {
	if err := s.lookupHostErr(s.disconnectErrs, hostName); err != nil {
		return err
	}
	return nil
}

func (s *stubPaneTransport) ReconnectHost(hostName string, sessionName string) error {
	if err := s.lookupHostErr(s.reconnectErrs, hostName); err != nil {
		return err
	}
	if sessionName == "" {
		return fmt.Errorf("missing session name")
	}
	return nil
}

func (s *stubPaneTransport) Shutdown() {
	s.shutdownCalls++
}

func (s *stubPaneTransport) AttachForTakeover(hostName, sshAddr, sshUser, remoteUID, sessionName string, paneMappings map[uint32]uint32) error {
	copied := make(map[uint32]uint32, len(paneMappings))
	for localPaneID, remotePaneID := range paneMappings {
		copied[localPaneID] = remotePaneID
	}
	s.attachCalls = append(s.attachCalls, attachForTakeoverCall{
		hostName:    hostName,
		sshAddr:     sshAddr,
		sshUser:     sshUser,
		remoteUID:   remoteUID,
		sessionName: sessionName,
		paneMap:     copied,
	})
	return s.attachErr
}

func (s *stubPaneTransport) DeployToAddress(hostName, sshAddr, sshUser string) {
	s.deployCalls = append(s.deployCalls, deployCall{
		hostName: hostName,
		sshAddr:  sshAddr,
		sshUser:  sshUser,
	})
}

func (s *stubTakeoverOnlyTransport) AttachForTakeover(hostName, sshAddr, sshUser, remoteUID, sessionName string, paneMappings map[uint32]uint32) error {
	copied := make(map[uint32]uint32, len(paneMappings))
	for localPaneID, remotePaneID := range paneMappings {
		copied[localPaneID] = remotePaneID
	}
	s.attachCalls = append(s.attachCalls, attachForTakeoverCall{
		hostName:    hostName,
		sshAddr:     sshAddr,
		sshUser:     sshUser,
		remoteUID:   remoteUID,
		sessionName: sessionName,
		paneMap:     copied,
	})
	return s.attachErr
}

func (s *stubTakeoverOnlyTransport) DeployToAddress(hostName, sshAddr, sshUser string) {
	s.deployCalls = append(s.deployCalls, deployCall{
		hostName: hostName,
		sshAddr:  sshAddr,
		sshUser:  sshUser,
	})
}

func (s *stubPaneTransport) lookupHostErr(errs map[string]error, hostName string) error {
	if errs == nil {
		return nil
	}
	return errs[hostName]
}

func installTestPaneTransport(t testingT, sess *Session, transport proto.PaneTransport, hostColor func(string) string) {
	t.Helper()
	sess.configurePaneTransport(transport, hostColor)
}

type testingT interface {
	Helper()
}
