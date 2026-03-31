package server

import (
	"slices"
	"time"

	caputil "github.com/weill-labs/amux/internal/capture"
	"github.com/weill-labs/amux/internal/eventloop"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

const (
	EventLayout           = eventloop.EventLayout
	EventOutput           = eventloop.EventOutput
	EventTerminal         = eventloop.EventTerminal
	EventIdle             = eventloop.EventIdle
	EventBusy             = eventloop.EventBusy
	EventExited           = eventloop.EventExited
	EventClientConnect    = eventloop.EventClientConnect
	EventClientDisconnect = eventloop.EventClientDisconnect
	EventPaneExit         = eventloop.EventPaneExit
)

const (
	DisconnectReasonServerReload   = eventloop.DisconnectReasonServerReload
	DisconnectReasonExplicitDetach = eventloop.DisconnectReasonExplicitDetach
	DisconnectReasonSocketError    = eventloop.DisconnectReasonSocketError
)

const DefaultEventThrottle = eventloop.DefaultEventThrottle

type Event = eventloop.Event
type eventSub = eventloop.Subscriber
type eventFilter = eventloop.Filter

// currentStateEvents builds synthetic events representing the current session
// state. This allows new subscribers to receive the current state immediately
// without missing events that occurred before subscription. All events are
// stamped with the current timestamp.
func (s *Session) currentStateEvents() []Event {
	idleSnap := s.snapshotIdleState()

	now := s.clock().Now().UTC().Format(time.RFC3339Nano)
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
