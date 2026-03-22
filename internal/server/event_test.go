package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func TestEventFilterMatchesAll(t *testing.T) {
	t.Parallel()
	f := eventFilter{}
	ev := Event{Type: EventLayout, PaneName: "pane-1", Host: "local"}
	if !f.matches(ev) {
		t.Error("empty filter should match all events")
	}
}

func TestEventFilterMatchesType(t *testing.T) {
	t.Parallel()
	f := eventFilter{Types: []string{EventLayout, EventIdle}}

	if !f.matches(Event{Type: EventLayout}) {
		t.Error("should match layout")
	}
	if !f.matches(Event{Type: EventIdle}) {
		t.Error("should match idle")
	}
	if f.matches(Event{Type: EventOutput}) {
		t.Error("should not match output")
	}
}

func TestEventFilterMatchesPane(t *testing.T) {
	t.Parallel()
	f := eventFilter{PaneName: "pane-1"}

	if !f.matches(Event{Type: EventIdle, PaneName: "pane-1"}) {
		t.Error("should match pane-1")
	}
	if f.matches(Event{Type: EventIdle, PaneName: "pane-2"}) {
		t.Error("should not match pane-2")
	}
	// Layout events without pane name should not match pane filter
	if f.matches(Event{Type: EventLayout}) {
		t.Error("layout without pane should not match pane filter")
	}
}

func TestEventFilterMatchesHost(t *testing.T) {
	t.Parallel()
	f := eventFilter{Host: "gpu-box"}

	if !f.matches(Event{Type: EventOutput, Host: "gpu-box"}) {
		t.Error("should match gpu-box")
	}
	if f.matches(Event{Type: EventOutput, Host: "local"}) {
		t.Error("should not match local")
	}
}

func TestEventFilterCombined(t *testing.T) {
	t.Parallel()
	f := eventFilter{Types: []string{EventIdle}, PaneName: "pane-1"}

	if !f.matches(Event{Type: EventIdle, PaneName: "pane-1"}) {
		t.Error("should match idle+pane-1")
	}
	if f.matches(Event{Type: EventBusy, PaneName: "pane-1"}) {
		t.Error("should not match busy+pane-1")
	}
	if f.matches(Event{Type: EventIdle, PaneName: "pane-2"}) {
		t.Error("should not match idle+pane-2")
	}
}

func TestEventFilterMatchesClient(t *testing.T) {
	t.Parallel()
	f := eventFilter{ClientID: "client-2"}

	if !f.matches(Event{Type: proto.UIEventDisplayPanesShown, ClientID: "client-2"}) {
		t.Error("should match client-2")
	}
	if f.matches(Event{Type: proto.UIEventDisplayPanesShown, ClientID: "client-1"}) {
		t.Error("should not match client-1")
	}
	if f.matches(Event{Type: EventLayout}) {
		t.Error("session-wide event should not match client filter")
	}
}

func TestEventJSON(t *testing.T) {
	t.Parallel()
	ev := Event{
		Type:       EventLayout,
		Timestamp:  "2026-03-16T12:00:00.123Z",
		Generation: 5,
		ActivePane: "pane-1",
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Event
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Type != EventLayout {
		t.Errorf("type: got %q, want %q", decoded.Type, EventLayout)
	}
	if decoded.Generation != 5 {
		t.Errorf("generation: got %d, want 5", decoded.Generation)
	}
	if decoded.ActivePane != "pane-1" {
		t.Errorf("active_pane: got %q, want %q", decoded.ActivePane, "pane-1")
	}
}

func TestEventJSONOmitsZeroFields(t *testing.T) {
	t.Parallel()
	ev := Event{
		Type:     EventIdle,
		PaneID:   1,
		PaneName: "pane-1",
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]interface{}
	json.Unmarshal(data, &raw)

	if _, ok := raw["generation"]; ok {
		t.Error("generation should be omitted when zero")
	}
	if _, ok := raw["active_pane"]; ok {
		t.Error("active_pane should be omitted when empty")
	}
	if _, ok := raw["host"]; ok {
		t.Error("host should be omitted when empty")
	}
}

func TestEmitEventDelivery(t *testing.T) {
	t.Parallel()
	sess := newSession("test-emit")
	stopCrashCheckpointLoop(t, sess)

	res := sess.enqueueEventSubscribe(eventFilter{}, false)
	defer sess.enqueueEventUnsubscribe(res.sub)

	// Emit from within the event loop (emitEvent is event-loop-only).
	sess.enqueueCommandMutation(func(s *Session) commandMutationResult {
		s.emitEvent(Event{Type: EventLayout, Generation: 1})
		return commandMutationResult{}
	})

	select {
	case data := <-res.sub.ch:
		var ev Event
		if err := json.Unmarshal(data, &ev); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if ev.Type != EventLayout {
			t.Errorf("type: got %q, want %q", ev.Type, EventLayout)
		}
		if ev.Timestamp == "" {
			t.Error("timestamp should be set by emitEvent")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestEmitEventFiltered(t *testing.T) {
	t.Parallel()
	sess := newSession("test-filter")
	stopCrashCheckpointLoop(t, sess)

	res := sess.enqueueEventSubscribe(eventFilter{Types: []string{EventIdle}}, false)
	defer sess.enqueueEventUnsubscribe(res.sub)

	sess.enqueueCommandMutation(func(s *Session) commandMutationResult {
		s.emitEvent(Event{Type: EventLayout, Generation: 1})
		s.emitEvent(Event{Type: EventIdle, PaneID: 1, PaneName: "pane-1"})
		return commandMutationResult{}
	})

	select {
	case data := <-res.sub.ch:
		var ev Event
		json.Unmarshal(data, &ev)
		if ev.Type != EventIdle {
			t.Errorf("expected idle event, got %q", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for idle event")
	}

	// Verify no extra events
	select {
	case data := <-res.sub.ch:
		t.Errorf("unexpected event: %s", string(data))
	case <-time.After(50 * time.Millisecond):
		// good — no extra events
	}
}

func TestEmitEventDropsWhenFull(t *testing.T) {
	t.Parallel()
	sess := newSession("test-drop")
	stopCrashCheckpointLoop(t, sess)

	res := sess.enqueueEventSubscribe(eventFilter{}, false)

	// Fill the channel from within the event loop.
	sess.enqueueCommandMutation(func(s *Session) commandMutationResult {
		for i := 0; i < 64; i++ {
			s.emitEvent(Event{Type: EventOutput, PaneID: 1})
		}
		return commandMutationResult{}
	})

	// This should not block — event is dropped.
	done := make(chan struct{})
	go func() {
		sess.enqueueCommandMutation(func(s *Session) commandMutationResult {
			s.emitEvent(Event{Type: EventOutput, PaneID: 1})
			return commandMutationResult{}
		})
		close(done)
	}()

	select {
	case <-done:
		// good — didn't block
	case <-time.After(time.Second):
		t.Fatal("emitEvent blocked on full channel")
	}

	sess.enqueueEventUnsubscribe(res.sub)
}

func TestEmitEventAfterRemove(t *testing.T) {
	t.Parallel()
	sess := newSession("test-remove-race")
	stopCrashCheckpointLoop(t, sess)

	res := sess.enqueueEventSubscribe(eventFilter{}, false)
	sess.enqueueEventUnsubscribe(res.sub)

	// Emit after unsubscribe: the event loop processes both sequentially,
	// so by the time emitEvent runs the sub is already removed from the
	// slice. No panic, no stale delivery.
	sess.enqueueCommandMutation(func(s *Session) commandMutationResult {
		s.emitEvent(Event{Type: EventLayout, Generation: 1})
		return commandMutationResult{}
	})
}

func TestParseEventsArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		args         []string
		wantFilter   eventFilter
		wantThrottle time.Duration
	}{
		{"empty", nil, eventFilter{}, DefaultEventThrottle},
		{"filter", []string{"--filter", "layout,idle"}, eventFilter{Types: []string{"layout", "idle"}}, DefaultEventThrottle},
		{"pane", []string{"--pane", "pane-1"}, eventFilter{PaneName: "pane-1"}, DefaultEventThrottle},
		{"host", []string{"--host", "gpu-box"}, eventFilter{Host: "gpu-box"}, DefaultEventThrottle},
		{"client", []string{"--client", "client-2"}, eventFilter{ClientID: "client-2"}, DefaultEventThrottle},
		{"combined", []string{"--filter", "idle", "--pane", "pane-1", "--host", "local", "--client", "client-2"},
			eventFilter{Types: []string{"idle"}, PaneName: "pane-1", Host: "local", ClientID: "client-2"}, DefaultEventThrottle},
		{"throttle_custom", []string{"--throttle", "100ms"}, eventFilter{}, 100 * time.Millisecond},
		{"throttle_disabled", []string{"--throttle", "0s"}, eventFilter{}, 0},
		{"throttle_invalid", []string{"--throttle", "bogus"}, eventFilter{}, DefaultEventThrottle},
		{"throttle_with_filter", []string{"--filter", "output", "--throttle", "200ms"},
			eventFilter{Types: []string{"output"}}, 200 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseEventsArgs(tt.args)
			if len(got.filter.Types) != len(tt.wantFilter.Types) {
				t.Errorf("Types: got %v, want %v", got.filter.Types, tt.wantFilter.Types)
			}
			for i := range got.filter.Types {
				if got.filter.Types[i] != tt.wantFilter.Types[i] {
					t.Errorf("Types[%d]: got %q, want %q", i, got.filter.Types[i], tt.wantFilter.Types[i])
				}
			}
			if got.filter.PaneName != tt.wantFilter.PaneName {
				t.Errorf("PaneName: got %q, want %q", got.filter.PaneName, tt.wantFilter.PaneName)
			}
			if got.filter.Host != tt.wantFilter.Host {
				t.Errorf("Host: got %q, want %q", got.filter.Host, tt.wantFilter.Host)
			}
			if got.filter.ClientID != tt.wantFilter.ClientID {
				t.Errorf("ClientID: got %q, want %q", got.filter.ClientID, tt.wantFilter.ClientID)
			}
			if got.throttle != tt.wantThrottle {
				t.Errorf("throttle: got %v, want %v", got.throttle, tt.wantThrottle)
			}
		})
	}
}

func TestPeekOutputPaneID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		data   []byte
		wantID uint32
		wantOK bool
	}{
		{
			"output_event",
			[]byte(`{"type":"output","pane_id":3,"pane_name":"pane-3"}`),
			3,
			true,
		},
		{
			"layout_event",
			[]byte(`{"type":"layout","generation":5}`),
			0,
			false,
		},
		{
			"idle_event",
			[]byte(`{"type":"idle","pane_id":1,"pane_name":"pane-1"}`),
			0,
			false,
		},
		{
			"output_pane_zero",
			[]byte(`{"type":"output","pane_id":0}`),
			0,
			true,
		},
		{
			"empty",
			[]byte{},
			0,
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			id, ok := peekOutputPaneID(tt.data)
			if ok != tt.wantOK {
				t.Errorf("ok: got %v, want %v", ok, tt.wantOK)
			}
			if id != tt.wantID {
				t.Errorf("id: got %d, want %d", id, tt.wantID)
			}
		})
	}
}

func TestCurrentStateEventsIncludesClientUIState(t *testing.T) {
	t.Parallel()

	sess := newSession("test-ui-state")
	stopCrashCheckpointLoop(t, sess)
	sess.clients = append(sess.clients,
		&clientConn{ID: "client-1", inputIdle: true},
		&clientConn{ID: "client-2", displayPanesShown: true, inputIdle: true},
	)

	events := sess.currentStateEvents()
	var got []Event
	for _, ev := range events {
		if ev.ClientID != "" {
			got = append(got, ev)
		}
	}

	if len(got) != 12 {
		t.Fatalf("got %d client UI events, want 12", len(got))
	}
	if got[0].Type != proto.UIEventDisplayPanesHidden || got[0].ClientID != "client-1" {
		t.Fatalf("first client UI event = %#v, want hidden for client-1", got[0])
	}
	if got[1].Type != proto.UIEventPrefixMessageHidden || got[1].ClientID != "client-1" {
		t.Fatalf("second client UI event = %#v, want prefix-message-hidden for client-1", got[1])
	}
	if got[2].Type != proto.UIEventCopyModeHidden || got[2].ClientID != "client-1" {
		t.Fatalf("third client UI event = %#v, want copy-mode-hidden for client-1", got[2])
	}
	if got[3].Type != proto.UIEventInputIdle || got[3].ClientID != "client-1" {
		t.Fatalf("fourth client UI event = %#v, want input-idle for client-1", got[3])
	}
	if got[4].Type != proto.UIEventChooseTreeHidden || got[4].ClientID != "client-1" {
		t.Fatalf("fifth client UI event = %#v, want choose-tree-hidden for client-1", got[4])
	}
	if got[5].Type != proto.UIEventChooseWindowHidden || got[5].ClientID != "client-1" {
		t.Fatalf("sixth client UI event = %#v, want choose-window-hidden for client-1", got[5])
	}
	if got[6].Type != proto.UIEventDisplayPanesShown || got[6].ClientID != "client-2" {
		t.Fatalf("seventh client UI event = %#v, want shown for client-2", got[6])
	}
	if got[7].Type != proto.UIEventPrefixMessageHidden || got[7].ClientID != "client-2" {
		t.Fatalf("eighth client UI event = %#v, want prefix-message-hidden for client-2", got[7])
	}
	if got[8].Type != proto.UIEventCopyModeHidden || got[8].ClientID != "client-2" {
		t.Fatalf("ninth client UI event = %#v, want copy-mode-hidden for client-2", got[8])
	}
	if got[9].Type != proto.UIEventInputIdle || got[9].ClientID != "client-2" {
		t.Fatalf("tenth client UI event = %#v, want input-idle for client-2", got[9])
	}
	if got[10].Type != proto.UIEventChooseTreeHidden || got[10].ClientID != "client-2" {
		t.Fatalf("eleventh client UI event = %#v, want choose-tree-hidden for client-2", got[10])
	}
	if got[11].Type != proto.UIEventChooseWindowHidden || got[11].ClientID != "client-2" {
		t.Fatalf("twelfth client UI event = %#v, want choose-window-hidden for client-2", got[11])
	}
}
