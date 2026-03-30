package server

import (
	"os"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

// PaneTransportHooks are the server-owned callbacks a transport uses to route
// remote output, exits, and state changes back into the session actor.
type PaneTransportHooks struct {
	OnPaneOutput  func(localPaneID uint32, data []byte)
	OnPaneExit    func(localPaneID uint32, reason string)
	OnStateChange func(hostName string, state proto.ConnState)
}

// PaneTransportFactory builds one transport for a session using the session's
// actor-safe callback hooks.
type PaneTransportFactory func(PaneTransportHooks) proto.PaneTransport

// PaneTakeoverTransport is the narrower transport surface needed only for SSH
// takeover attach/deploy flows.
type PaneTakeoverTransport interface {
	AttachForTakeover(hostName, sshAddr, sshUser, remoteUID, sessionName string, paneMappings map[uint32]uint32) error
	DeployToAddress(hostName, sshAddr, sshUser string)
}

// PaneTakeoverFactory builds a takeover-only transport for sessions that do
// not have a general remote pane transport configured.
type PaneTakeoverFactory func(PaneTransportHooks) PaneTakeoverTransport

func (s *Session) paneTransportHooks() PaneTransportHooks {
	return PaneTransportHooks{
		OnPaneOutput: func(localPaneID uint32, data []byte) {
			pane, err := enqueueSessionQuery(s, func(s *Session) (*mux.Pane, error) {
				return s.findPaneByID(localPaneID), nil
			})
			if err != nil || pane == nil {
				return
			}
			pane.FeedOutput(data)
		},
		OnPaneExit: func(localPaneID uint32, reason string) {
			if s.shutdown.Load() {
				return
			}
			s.enqueueRemotePaneExit(localPaneID, reason)
		},
		OnStateChange: func(hostName string, state proto.ConnState) {
			s.enqueueRemoteStateChange(hostName, state)
		},
	}
}

func (s *Session) configurePaneTransport(transport proto.PaneTransport, hostColor func(string) string) {
	s.RemoteManager = transport
	s.remoteHostColor = hostColor
	s.remoteTakeover = nil
	if takeover, ok := transport.(PaneTakeoverTransport); ok {
		s.remoteTakeover = takeover
	}
}

func (s *Session) configurePaneTakeover(transport PaneTakeoverTransport) {
	s.remoteTakeover = transport
	if paneTransport, ok := transport.(proto.PaneTransport); ok {
		s.RemoteManager = paneTransport
	}
}

func (s *Session) remotePaneColor(hostName string) string {
	if s.remoteHostColor != nil {
		return s.remoteHostColor(hostName)
	}
	return config.ColorForHost(hostName)
}

func (s *Session) remoteWriteOverride(paneID uint32) func([]byte) (int, error) {
	return func(data []byte) (int, error) {
		if s.RemoteManager != nil {
			return len(data), s.RemoteManager.SendInput(paneID, data)
		}
		// Restored proxy panes may exist before the transport is reinstalled.
		// Drop input until the transport is ready again.
		return len(data), nil
	}
}

func managedSessionName(localSessionName string) string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	return localSessionName + "@" + hostname
}

// SetupPaneTransport installs per-session transports without importing the
// concrete transport package into the server package.
func (s *Server) SetupPaneTransport(hostColor func(string) string, factory PaneTransportFactory) {
	if s == nil || factory == nil {
		return
	}
	for _, sess := range s.sessions {
		sess.configurePaneTransport(factory(sess.paneTransportHooks()), hostColor)
	}
}

// SetupPaneTakeoverTransport installs the takeover-only transport used for SSH
// takeover flows when no generic remote pane transport is configured.
func (s *Server) SetupPaneTakeoverTransport(factory PaneTakeoverFactory) {
	if s == nil || factory == nil {
		return
	}
	for _, sess := range s.sessions {
		sess.configurePaneTakeover(factory(sess.paneTransportHooks()))
	}
}
