package server

import (
	"slices"
	"time"
)

// Event types emitted by the event stream.
const (
	EventLayout = "layout"
	EventOutput = "output"
	EventIdle   = "idle"
	EventBusy   = "busy"
	EventHook   = "hook"
)

// Event is a single event in the NDJSON event stream.
type Event struct {
	Type       string `json:"type"`
	Timestamp  string `json:"ts"`
	Generation uint64 `json:"generation,omitempty"`
	PaneID     uint32 `json:"pane_id,omitempty"`
	PaneName   string `json:"pane_name,omitempty"`
	Host       string `json:"host,omitempty"`
	ActivePane string `json:"active_pane,omitempty"`
	ClientID   string `json:"client_id,omitempty"`
	HookEvent  string `json:"hook_event,omitempty"`
	Command    string `json:"command,omitempty"`
	Success    bool   `json:"success,omitempty"`
	Error      string `json:"error,omitempty"`
}

// eventSub is a subscriber to the event stream.
type eventSub struct {
	ch     chan []byte // buffered, JSON-encoded events (one line per event)
	filter eventFilter
}

// eventFilter controls which events a subscriber receives.
type eventFilter struct {
	Types    []string // event types to include (empty = all)
	PaneName string   // match pane name (empty = all panes)
	Host     string   // match host (empty = all hosts)
	ClientID string   // match client ID (empty = all clients)
}

// matches returns true if the event passes the filter.
func (f eventFilter) matches(ev Event) bool {
	if len(f.Types) > 0 && !slices.Contains(f.Types, ev.Type) {
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

// currentStateEvents builds synthetic events representing the current session
// state. This allows new subscribers to receive the current state immediately
// without missing events that occurred before subscription. All events are
// stamped with the current timestamp.
func (s *Session) currentStateEvents() []Event {
	// Snapshot idle state before acquiring s.mu (lock ordering: idle.mu before s.mu).
	idleSnap := s.idle.SnapshotState()

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	var events []Event

	// Current layout state
	w := s.ActiveWindow()
	activePaneName := ""
	if w != nil && w.ActivePane != nil {
		activePaneName = w.ActivePane.Meta.Name
	}
	events = append(events, Event{
		Type:       EventLayout,
		Timestamp:  now,
		Generation: s.generation.Load(),
		ActivePane: activePaneName,
	})

	// Current idle/busy state for each pane
	for _, p := range s.Panes {
		evType := EventBusy
		if idleSnap[p.ID] {
			evType = EventIdle
		}
		events = append(events, Event{
			Type:      evType,
			Timestamp: now,
			PaneID:    p.ID,
			PaneName:  p.Meta.Name,
			Host:      p.Meta.Host,
		})
	}

	for _, cc := range s.clients {
		for _, ev := range cc.currentUIEvents() {
			ev.Timestamp = now
			events = append(events, ev)
		}
	}

	return events
}
