package eventloop

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

// Event types emitted by the event stream.
const (
	EventLayout           = "layout"
	EventOutput           = "output"
	EventTerminal         = "terminal"
	EventIdle             = "idle"
	EventBusy             = "busy"
	EventExited           = "exited"
	EventClientConnect    = "client-connect"
	EventClientDisconnect = "client-disconnect"
	EventPaneExit         = "pane-exit"
)

const (
	DisconnectReasonServerReload   = "server-reload"
	DisconnectReasonExplicitDetach = "explicit-detach"
	DisconnectReasonSocketError    = "socket-error"
)

// DefaultEventThrottle is the default throttle interval for `amux events`.
// Output events are coalesced to at most one per pane per interval.
const DefaultEventThrottle = 50 * time.Millisecond

// Event is a single event in the NDJSON event stream.
type Event struct {
	Type       string                 `json:"type"`
	Timestamp  string                 `json:"ts"`
	Generation uint64                 `json:"generation,omitempty"`
	PaneID     uint32                 `json:"pane_id,omitempty"`
	PaneName   string                 `json:"pane_name,omitempty"`
	Host       string                 `json:"host,omitempty"`
	ActivePane string                 `json:"active_pane,omitempty"`
	ClientID   string                 `json:"client_id,omitempty"`
	Reason     string                 `json:"reason,omitempty"`
	Cursor     *proto.CaptureCursor   `json:"cursor,omitempty"`
	Terminal   *proto.CaptureTerminal `json:"terminal,omitempty"`
}

// Subscriber is a subscriber to the event stream.
type Subscriber struct {
	Ch     chan []byte
	Filter Filter
}

// Filter controls which events a subscriber receives.
type Filter struct {
	Types    []string
	PaneID   uint32
	PaneName string
	Host     string
	ClientID string
}

// Matches returns true if the event passes the filter.
func (f Filter) Matches(ev Event) bool {
	if len(f.Types) > 0 && !slices.Contains(f.Types, ev.Type) {
		return false
	}
	if f.PaneID != 0 && ev.PaneID != f.PaneID {
		return false
	}
	if f.PaneName != "" && ev.PaneName != f.PaneName {
		return false
	}
	if f.Host != "" && ev.Host != f.Host {
		return false
	}
	if f.ClientID != "" && ev.ClientID != f.ClientID {
		return false
	}
	return true
}

// MarshalMatching encodes the subset of events that match the filter.
func MarshalMatching(events []Event, filter Filter) [][]byte {
	encoded := make([][]byte, 0, len(events))
	for _, ev := range events {
		if !filter.Matches(ev) {
			continue
		}
		data, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		encoded = append(encoded, data)
	}
	return encoded
}
