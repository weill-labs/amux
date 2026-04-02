package mouse

import (
	"strings"
	"testing"
)

func TestFlushPendingReturnsIncompleteSequence(t *testing.T) {
	t.Parallel()

	p := &Parser{}
	if _, ok, flushed := p.Feed(0x1b); ok || flushed != nil {
		t.Fatalf("Feed(ESC) = ok=%v flushed=%q, want in-progress state only", ok, flushed)
	}
	if got := string(p.FlushPending()); got != "\x1b" {
		t.Fatalf("FlushPending() = %q, want ESC", got)
	}
	if p.InProgress() {
		t.Fatal("parser should not remain in progress after FlushPending")
	}
	if got := p.FlushPending(); got != nil {
		t.Fatalf("second FlushPending() = %q, want nil", got)
	}
}

func TestFlushPendingKeepsSplitKittyCSISequence(t *testing.T) {
	t.Parallel()

	p := &Parser{}
	events, flushed := feedAll(t, p, []byte("\x1b[114;"))
	if len(events) != 0 {
		t.Fatalf("events = %v, want none for incomplete kitty CSI", events)
	}
	if len(flushed) != 0 {
		t.Fatalf("flushed = %q, want incomplete kitty CSI to stay buffered", flushed)
	}
	if got := p.FlushPending(); got != nil {
		t.Fatalf("FlushPending() = %q, want nil for incomplete kitty CSI", got)
	}
	if !p.InProgress() {
		t.Fatal("parser should remain in progress for incomplete kitty CSI")
	}

	events, flushed = feedAll(t, p, []byte("5u"))
	if len(events) != 0 {
		t.Fatalf("events = %v, want none for non-mouse kitty CSI", events)
	}
	if got := string(flushed); got != "\x1b[114;5u" {
		t.Fatalf("flushed = %q, want complete kitty CSI", got)
	}
	if p.InProgress() {
		t.Fatal("parser should reset after complete kitty CSI")
	}
}

func TestFlushPendingKeepsSplitMouseSequence(t *testing.T) {
	t.Parallel()

	p := &Parser{}
	events, flushed := feedAll(t, p, []byte("\x1b[<32;10;"))
	if len(events) != 0 {
		t.Fatalf("events = %v, want none for incomplete mouse sequence", events)
	}
	if len(flushed) != 0 {
		t.Fatalf("flushed = %q, want incomplete mouse sequence to stay buffered", flushed)
	}
	if got := p.FlushPending(); got != nil {
		t.Fatalf("FlushPending() = %q, want nil for incomplete mouse sequence", got)
	}
	if !p.InProgress() {
		t.Fatal("parser should remain in progress for incomplete mouse sequence")
	}

	events, flushed = feedAll(t, p, []byte("5M"))
	if len(flushed) != 0 {
		t.Fatalf("flushed = %q, want no flushed bytes after completing mouse sequence", flushed)
	}
	if len(events) != 1 {
		t.Fatalf("events = %v, want one mouse event", events)
	}
	if events[0].Action != Motion {
		t.Fatalf("action = %v, want %v", events[0].Action, Motion)
	}
	if events[0].Button != ButtonLeft {
		t.Fatalf("button = %v, want %v", events[0].Button, ButtonLeft)
	}
	if events[0].X != 9 || events[0].Y != 4 {
		t.Fatalf("position = (%d,%d), want (9,4)", events[0].X, events[0].Y)
	}
	if p.InProgress() {
		t.Fatal("parser should reset after complete mouse sequence")
	}
}

func TestFeedFlushesRunawayCSIAndMouseParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "non-mouse csi longer than limit",
			input: "\x1b[" + strings.Repeat("1", 70),
		},
		{
			name:  "mouse params longer than limit",
			input: "\x1b[<" + strings.Repeat("1", 40),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := &Parser{}
			events, flushed := feedAll(t, p, []byte(tt.input))
			if len(events) != 0 {
				t.Fatalf("events = %v, want none", events)
			}
			if got := string(flushed); got != tt.input {
				t.Fatalf("flushed = %q, want %q", got, tt.input)
			}
			if p.InProgress() {
				t.Fatal("parser should reset after runaway flush")
			}
		})
	}
}

func TestMalformedMouseSequenceIsDropped(t *testing.T) {
	t.Parallel()

	p := &Parser{}
	events, flushed := feedAll(t, p, []byte("\x1b[<0;10M"))
	if len(events) != 0 {
		t.Fatalf("events = %v, want none for malformed params", events)
	}
	if len(flushed) != 0 {
		t.Fatalf("flushed = %q, want malformed mouse sequence to be dropped", flushed)
	}
	if p.InProgress() {
		t.Fatal("parser should reset after malformed mouse sequence")
	}
}

func TestParseScrollLeftRightAndButtonNames(t *testing.T) {
	t.Parallel()

	p := &Parser{}
	events, _ := feedAll(t, p, []byte("\x1b[<66;4;5M\x1b[<67;6;7M"))
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Button != ScrollLeft {
		t.Fatalf("first button = %v, want %v", events[0].Button, ScrollLeft)
	}
	if events[1].Button != ScrollRight {
		t.Fatalf("second button = %v, want %v", events[1].Button, ScrollRight)
	}

	tests := []struct {
		button Button
		want   string
	}{
		{ButtonNone, "none"},
		{ScrollLeft, "scroll-left"},
		{ScrollRight, "scroll-right"},
		{Button(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.button.String(); got != tt.want {
			t.Fatalf("Button(%d).String() = %q, want %q", tt.button, got, tt.want)
		}
	}
}
