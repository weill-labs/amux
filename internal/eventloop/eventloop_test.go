package eventloop

import (
	"encoding/json"
	"errors"
	"testing"
)

type counterState struct {
	value int
}

type addCommand struct {
	delta int
}

func (c addCommand) Handle(s *counterState) {
	s.value += c.delta
}

func TestFilterMatchesAll(t *testing.T) {
	t.Parallel()

	filter := Filter{}
	ev := Event{Type: EventLayout, PaneName: "pane-1", Host: "local"}
	if !filter.Matches(ev) {
		t.Fatal("empty filter should match all events")
	}
}

func TestFilterMatchesTypeAndPane(t *testing.T) {
	t.Parallel()

	filter := Filter{Types: []string{EventLayout, EventIdle}, PaneName: "pane-1"}
	if !filter.Matches(Event{Type: EventIdle, PaneName: "pane-1"}) {
		t.Fatal("filter should match idle event for pane-1")
	}
	if filter.Matches(Event{Type: EventOutput, PaneName: "pane-1"}) {
		t.Fatal("filter should reject unmatched type")
	}
	if filter.Matches(Event{Type: EventIdle, PaneName: "pane-2"}) {
		t.Fatal("filter should reject unmatched pane")
	}
}

func TestEventJSONOmitsZeroFields(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(Event{Type: EventIdle, PaneID: 1, PaneName: "pane-1"})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if _, ok := raw["generation"]; ok {
		t.Fatal("generation should be omitted when zero")
	}
	if _, ok := raw["active_pane"]; ok {
		t.Fatal("active_pane should be omitted when empty")
	}
}

func TestMarshalMatchingEncodesOnlyMatchingEvents(t *testing.T) {
	t.Parallel()

	events := []Event{
		{Type: EventLayout},
		{Type: EventIdle, PaneName: "pane-1"},
		{Type: EventIdle, PaneName: "pane-2"},
	}

	encoded := MarshalMatching(events, Filter{Types: []string{EventIdle}, PaneName: "pane-1"})
	if len(encoded) != 1 {
		t.Fatalf("MarshalMatching() count = %d, want 1", len(encoded))
	}

	var ev Event
	if err := json.Unmarshal(encoded[0], &ev); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if ev.Type != EventIdle || ev.PaneName != "pane-1" {
		t.Fatalf("decoded event = %+v, want idle pane-1", ev)
	}
}

func TestEmitDeliversOnlyMatchingSubscribers(t *testing.T) {
	t.Parallel()

	var subs []*Subscriber
	idleSub := Subscribe(&subs, Filter{Types: []string{EventIdle}})
	layoutSub := Subscribe(&subs, Filter{Types: []string{EventLayout}})

	Emit(subs, Event{Type: EventIdle, PaneName: "pane-1"})

	select {
	case data := <-idleSub.Ch:
		var ev Event
		if err := json.Unmarshal(data, &ev); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if ev.Timestamp == "" {
			t.Fatal("Emit() should stamp timestamps")
		}
		if ev.Type != EventIdle {
			t.Fatalf("emitted type = %q, want %q", ev.Type, EventIdle)
		}
	default:
		t.Fatal("idle subscriber did not receive event")
	}

	select {
	case data := <-layoutSub.Ch:
		t.Fatalf("layout subscriber received unexpected event: %s", string(data))
	default:
	}
}

func TestUnsubscribeRemovesSubscriber(t *testing.T) {
	t.Parallel()

	var subs []*Subscriber
	sub := Subscribe(&subs, Filter{})
	Unsubscribe(&subs, sub)

	Emit(subs, Event{Type: EventLayout})

	select {
	case data := <-sub.Ch:
		t.Fatalf("unsubscribed subscriber received event: %s", string(data))
	default:
	}
}

func TestRunProcessesCommands(t *testing.T) {
	t.Parallel()

	state := &counterState{}
	queue := make(chan Command[counterState], 2)
	stop := make(chan struct{})
	done := make(chan struct{})

	go Run(state, queue, stop, done, nil)

	if !Enqueue(queue, stop, addCommand{delta: 2}) {
		t.Fatal("Enqueue(first) = false, want true")
	}
	if !Enqueue(queue, stop, addCommand{delta: 3}) {
		t.Fatal("Enqueue(second) = false, want true")
	}

	result, err := EnqueueQuery(queue, stop, done, func(s *counterState) (int, error) {
		return s.value, nil
	}, nil, ErrStopped)
	if err != nil {
		t.Fatalf("EnqueueQuery() = %v, want nil", err)
	}
	if result != 5 {
		t.Fatalf("state value = %d, want 5", result)
	}

	close(stop)
	<-done
}

func TestEnqueueReturnsFalseAfterStop(t *testing.T) {
	t.Parallel()

	queue := make(chan Command[counterState], 1)
	stop := make(chan struct{})
	close(stop)

	if Enqueue(queue, stop, addCommand{delta: 1}) {
		t.Fatal("Enqueue() = true after stop, want false")
	}
}

func TestEnqueueQueryRecoversPanics(t *testing.T) {
	t.Parallel()

	state := &counterState{}
	queue := make(chan Command[counterState], 1)
	stop := make(chan struct{})
	done := make(chan struct{})

	go Run(state, queue, stop, done, nil)

	_, err := EnqueueQuery(queue, stop, done, func(*counterState) (int, error) {
		panic("boom")
	}, func(r any, _ []byte) error {
		return errors.New("internal error: recovered boom")
	}, ErrStopped)
	if err == nil || err.Error() != "internal error: recovered boom" {
		t.Fatalf("EnqueueQuery() error = %v, want recovered panic error", err)
	}

	close(stop)
	<-done
}

func TestEnqueueQueryReturnsShutdownErrorWhenLoopDone(t *testing.T) {
	t.Parallel()

	queue := make(chan Command[counterState], 1)
	stop := make(chan struct{})
	done := make(chan struct{})
	close(done)

	_, err := EnqueueQuery(queue, stop, done, func(*counterState) (int, error) {
		return 0, nil
	}, nil, ErrStopped)
	if !errors.Is(err, ErrStopped) {
		t.Fatalf("EnqueueQuery() error = %v, want %v", err, ErrStopped)
	}
}
