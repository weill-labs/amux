package proto

import (
	"testing"
	"time"
)

type testPaneTransport struct{}

func (testPaneTransport) SendInput(uint32, []byte) error                    { return nil }
func (testPaneTransport) SendResize(uint32, int, int) error                 { return nil }
func (testPaneTransport) KillPane(uint32, bool, time.Duration) error        { return nil }
func (testPaneTransport) RemovePane(uint32)                                 {}
func (testPaneTransport) CreatePane(string, uint32, string) (uint32, error) { return 0, nil }
func (testPaneTransport) ConnStatusForPane(uint32) string                   { return "" }
func (testPaneTransport) HostStatus(string) ConnState                       { return Disconnected }
func (testPaneTransport) AllHostStatus() map[string]ConnState               { return map[string]ConnState{} }
func (testPaneTransport) DisconnectHost(string) error                       { return nil }
func (testPaneTransport) ReconnectHost(string, string) error                { return nil }
func (testPaneTransport) Shutdown()                                         {}

func TestConnStateConstants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  ConnState
		want string
	}{
		{name: "disconnected", got: Disconnected, want: "disconnected"},
		{name: "connecting", got: Connecting, want: "connecting"},
		{name: "connected", got: Connected, want: "connected"},
		{name: "reconnecting", got: Reconnecting, want: "reconnecting"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := string(tt.got); got != tt.want {
				t.Fatalf("string(%s) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestPaneTransportInterfaceShape(t *testing.T) {
	t.Parallel()

	var transport PaneTransport = testPaneTransport{}
	if transport == nil {
		t.Fatal("PaneTransport should accept a concrete transport implementation")
	}
}
