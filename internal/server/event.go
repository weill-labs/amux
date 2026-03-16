package server

import (
	"encoding/json"
	"slices"
	"time"
)

// Event types emitted by the event stream.
const (
	EventLayout = "layout"
	EventOutput = "output"
	EventIdle   = "idle"
	EventBusy   = "busy"
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
	return true
}

// addEventSub registers a new event subscriber and returns it.
func (s *Session) addEventSub(f eventFilter) *eventSub {
	sub := &eventSub{
		ch:     make(chan []byte, 64),
		filter: f,
	}
	s.eventSubsMu.Lock()
	s.eventSubs = append(s.eventSubs, sub)
	s.eventSubsMu.Unlock()
	return sub
}

// removeEventSub unregisters a subscriber and closes its channel.
func (s *Session) removeEventSub(sub *eventSub) {
	s.eventSubsMu.Lock()
	s.eventSubs = slices.DeleteFunc(s.eventSubs, func(e *eventSub) bool { return e == sub })
	s.eventSubsMu.Unlock()
	close(sub.ch)
}

// emitEvent marshals an event to JSON and sends it to all matching subscribers.
// Non-blocking: if a subscriber's channel is full, the event is dropped.
func (s *Session) emitEvent(ev Event) {
	ev.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}

	s.eventSubsMu.Lock()
	subs := make([]*eventSub, len(s.eventSubs))
	copy(subs, s.eventSubs)
	s.eventSubsMu.Unlock()

	for _, sub := range subs {
		if sub.filter.matches(ev) {
			select {
			case sub.ch <- data:
			default: // drop if full
			}
		}
	}
}

// currentStateEvents builds synthetic events representing the current session
// state. This allows new subscribers to receive the current state immediately
// without missing events that occurred before subscription. All events are
// stamped with the current timestamp.
func (s *Session) currentStateEvents() []Event {
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
	s.idleTimerMu.Lock()
	for _, p := range s.Panes {
		evType := EventBusy
		if s.idleState[p.ID] {
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
	s.idleTimerMu.Unlock()

	return events
}
