package eventloop

import (
	"encoding/json"
	"time"
)

func Subscribe(subs *[]*Subscriber, filter Filter) *Subscriber {
	sub := &Subscriber{
		Ch:     make(chan []byte, 64),
		Filter: filter,
	}
	*subs = append(*subs, sub)
	return sub
}

func Unsubscribe(subs *[]*Subscriber, target *Subscriber) {
	for i, sub := range *subs {
		if sub == target {
			*subs = append((*subs)[:i], (*subs)[i+1:]...)
			return
		}
	}
}

// Emit marshals an event and sends it to all matching subscribers.
// If a subscriber channel is full the event is dropped.
func Emit(subs []*Subscriber, ev Event) {
	ev.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	for _, sub := range subs {
		if !sub.Filter.Matches(ev) {
			continue
		}
		select {
		case sub.Ch <- data:
		default:
		}
	}
}
