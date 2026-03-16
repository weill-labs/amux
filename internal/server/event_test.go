package server

import (
	"encoding/json"
	"testing"
	"time"
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

	sub := sess.addEventSub(eventFilter{})
	defer sess.removeEventSub(sub)

	sess.emitEvent(Event{Type: EventLayout, Generation: 1})

	select {
	case data := <-sub.ch:
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

	sub := sess.addEventSub(eventFilter{Types: []string{EventIdle}})
	defer sess.removeEventSub(sub)

	sess.emitEvent(Event{Type: EventLayout, Generation: 1})
	sess.emitEvent(Event{Type: EventIdle, PaneID: 1, PaneName: "pane-1"})

	select {
	case data := <-sub.ch:
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
	case data := <-sub.ch:
		t.Errorf("unexpected event: %s", string(data))
	case <-time.After(50 * time.Millisecond):
		// good — no extra events
	}
}

func TestEmitEventDropsWhenFull(t *testing.T) {
	t.Parallel()
	sess := newSession("test-drop")

	sub := sess.addEventSub(eventFilter{})

	// Fill the channel
	for i := 0; i < 64; i++ {
		sess.emitEvent(Event{Type: EventOutput, PaneID: 1})
	}

	// This should not block — event is dropped
	done := make(chan struct{})
	go func() {
		sess.emitEvent(Event{Type: EventOutput, PaneID: 1})
		close(done)
	}()

	select {
	case <-done:
		// good — didn't block
	case <-time.After(time.Second):
		t.Fatal("emitEvent blocked on full channel")
	}

	sess.removeEventSub(sub)
}

func TestEmitEventAfterRemove(t *testing.T) {
	t.Parallel()
	sess := newSession("test-remove-race")

	sub := sess.addEventSub(eventFilter{})
	sess.removeEventSub(sub)

	// emitEvent after removeEventSub must not panic (send on closed channel).
	// The trySend recover guard handles this race.
	sess.emitEvent(Event{Type: EventLayout, Generation: 1})
}

func TestParseEventsArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want eventFilter
	}{
		{"empty", nil, eventFilter{}},
		{"filter", []string{"--filter", "layout,idle"}, eventFilter{Types: []string{"layout", "idle"}}},
		{"pane", []string{"--pane", "pane-1"}, eventFilter{PaneName: "pane-1"}},
		{"host", []string{"--host", "gpu-box"}, eventFilter{Host: "gpu-box"}},
		{"combined", []string{"--filter", "idle", "--pane", "pane-1", "--host", "local"},
			eventFilter{Types: []string{"idle"}, PaneName: "pane-1", Host: "local"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseEventsArgs(tt.args)
			if len(got.Types) != len(tt.want.Types) {
				t.Errorf("Types: got %v, want %v", got.Types, tt.want.Types)
			}
			for i := range got.Types {
				if got.Types[i] != tt.want.Types[i] {
					t.Errorf("Types[%d]: got %q, want %q", i, got.Types[i], tt.want.Types[i])
				}
			}
			if got.PaneName != tt.want.PaneName {
				t.Errorf("PaneName: got %q, want %q", got.PaneName, tt.want.PaneName)
			}
			if got.Host != tt.want.Host {
				t.Errorf("Host: got %q, want %q", got.Host, tt.want.Host)
			}
		})
	}
}
