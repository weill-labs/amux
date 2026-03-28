package server

import (
	"slices"
	"time"

	caputil "github.com/weill-labs/amux/internal/capture"
	"github.com/weill-labs/amux/internal/mux"
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

// eventSub is a subscriber to the event stream.
type eventSub struct {
	ch     chan []byte // buffered, JSON-encoded events (one line per event)
	filter eventFilter
}

// eventFilter controls which events a subscriber receives.
type eventFilter struct {
	Types    []string // event types to include (empty = all)
	PaneID   uint32   // match pane ID (0 = all panes)
	PaneName string   // match pane name (empty = all panes)
	Host     string   // match host (empty = all hosts)
	ClientID string   // match client ID (empty = all clients)
}

// matches returns true if the event passes the filter.
func (f eventFilter) matches(ev Event) bool {
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

// currentStateEvents builds synthetic events representing the current session
// state. This allows new subscribers to receive the current state immediately
// without missing events that occurred before subscription. All events are
// stamped with the current timestamp.
func (s *Session) currentStateEvents() []Event {
	idleSnap := s.snapshotIdleState()

	snapshotNow := time.Now()
	now := snapshotNow.UTC().Format(time.RFC3339Nano)
	var events []Event

	// Current layout state
	w := s.activeWindow()
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

	// Current terminal state for each pane.
	for _, p := range s.Panes {
		ev := s.capturePaneTerminalEvent(p)
		ev.Timestamp = now
		events = append(events, ev)
	}

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
		if p.AgentStatus().Idle {
			events = append(events, Event{
				Type:      EventExited,
				Timestamp: now,
				PaneID:    p.ID,
				PaneName:  p.Meta.Name,
				Host:      p.Meta.Host,
			})
		}
	}

	for _, cc := range s.ensureClientManager().snapshotClients() {
		events = append(events, Event{
			Type:      EventClientConnect,
			Timestamp: now,
			ClientID:  cc.ID,
		})
		for _, ev := range cc.currentUIEvents() {
			ev.Timestamp = now
			events = append(events, ev)
		}
	}

	return events
}

type paneTerminalEventState struct {
	Cursor   proto.CaptureCursor
	Terminal *proto.CaptureTerminal
}

func (s *Session) capturePaneTerminalState(pane *mux.Pane) paneTerminalEventState {
	snap := pane.TerminalSnapshot()
	return paneTerminalEventState{
		Cursor:   caputil.CursorFromState(snap.CursorCol, snap.CursorRow, snap.CursorHidden, snap.Terminal),
		Terminal: caputil.TerminalFromState(snap.Terminal),
	}
}

func (s *Session) ensureTerminalEventState() map[uint32]paneTerminalEventState {
	if s.terminalEventState == nil {
		s.terminalEventState = make(map[uint32]paneTerminalEventState)
	}
	return s.terminalEventState
}

func (s *Session) capturePaneTerminalEvent(pane *mux.Pane) Event {
	state := s.capturePaneTerminalState(pane)
	s.ensureTerminalEventState()[pane.ID] = state
	return paneTerminalEvent(pane, state)
}

func paneTerminalEvent(pane *mux.Pane, state paneTerminalEventState) Event {
	return Event{
		Type:     EventTerminal,
		PaneID:   pane.ID,
		PaneName: pane.Meta.Name,
		Host:     pane.Meta.Host,
		Cursor:   &state.Cursor,
		Terminal: state.Terminal,
	}
}

func paneTerminalEventStateEqual(left, right paneTerminalEventState) bool {
	return left.Cursor == right.Cursor && captureTerminalEqual(left.Terminal, right.Terminal)
}

func captureTerminalEqual(left, right *proto.CaptureTerminal) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.AltScreen == right.AltScreen &&
		left.ForegroundColor == right.ForegroundColor &&
		left.BackgroundColor == right.BackgroundColor &&
		left.CursorColor == right.CursorColor &&
		captureHyperlinkEqual(left.Hyperlink, right.Hyperlink) &&
		captureMouseEqual(left.Mouse, right.Mouse) &&
		slices.Equal(left.Palette, right.Palette)
}

func captureHyperlinkEqual(left, right *proto.CaptureHyperlink) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.URL == right.URL && left.Params == right.Params
}

func captureMouseEqual(left, right *proto.CaptureMouseProtocol) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Tracking == right.Tracking && left.SGR == right.SGR
}

func (s *Session) emitPaneTerminalEventIfChanged(pane *mux.Pane) {
	if pane == nil {
		return
	}
	state := s.capturePaneTerminalState(pane)
	stateByPane := s.ensureTerminalEventState()
	prev, ok := stateByPane[pane.ID]
	if ok && paneTerminalEventStateEqual(prev, state) {
		return
	}
	stateByPane[pane.ID] = state
	s.emitEvent(paneTerminalEvent(pane, state))
}
