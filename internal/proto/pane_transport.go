package proto

import "time"

// ConnState represents the connection state of a remote host.
type ConnState string

const (
	Disconnected ConnState = "disconnected"
	Connecting   ConnState = "connecting"
	Connected    ConnState = "connected"
	Reconnecting ConnState = "reconnecting"
)

// PaneTransport abstracts remote pane operations away from the server package.
// Nil means no remote transport is configured.
type PaneTransport interface {
	SendInput(localPaneID uint32, data []byte) error
	SendResize(localPaneID uint32, cols, rows int) error
	KillPane(localPaneID uint32, cleanup bool, timeout time.Duration) error
	RemovePane(localPaneID uint32)
	RegisterPane(hostName string, localPaneID uint32, remotePaneID uint32) error
	CreatePane(hostName string, localPaneID uint32, sessionName string) (uint32, error)
	ConnectHost(hostName string, sessionName string) (*LayoutSnapshot, error)
	ConnStatusForPane(localPaneID uint32) string
	HostStatus(hostName string) ConnState
	AllHostStatus() map[string]ConnState
	RunHostCommand(hostName string, sessionName string, cmdName string, cmdArgs []string) (string, error)
	DisconnectHost(hostName string) error
	ReconnectHost(hostName string, sessionName string) error
	Shutdown()
}
