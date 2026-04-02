package mouse

import (
	"fmt"
	"testing"
)

// feedAll pushes each byte into the parser and returns all events + flushed bytes.
func feedAll(t *testing.T, p *Parser, input []byte) ([]Event, []byte) {
	t.Helper()
	var events []Event
	var flushed []byte
	for _, b := range input {
		ev, ok, flush := p.Feed(b)
		if ok {
			events = append(events, ev)
		}
		flushed = append(flushed, flush...)
	}
	return events, flushed
}

func TestParseLeftClick(t *testing.T) {
	t.Parallel()

	p := &Parser{}
	// \033[<0;10;5M — left click at column 10, row 5 (1-based)
	events, flushed := feedAll(t, p, []byte("\033[<0;10;5M"))

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Button != ButtonLeft {
		t.Errorf("button: got %d, want ButtonLeft", ev.Button)
	}
	if ev.Action != Press {
		t.Errorf("action: got %d, want Press", ev.Action)
	}
	if ev.X != 9 || ev.Y != 4 {
		t.Errorf("position: got (%d,%d), want (9,4)", ev.X, ev.Y)
	}
	if len(flushed) != 0 {
		t.Errorf("unexpected flushed bytes: %v", flushed)
	}
}

func TestParseRightClick(t *testing.T) {
	t.Parallel()

	p := &Parser{}
	events, _ := feedAll(t, p, []byte("\033[<2;20;10M"))

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Button != ButtonRight {
		t.Errorf("button: got %d, want ButtonRight", events[0].Button)
	}
}

func TestParseRelease(t *testing.T) {
	t.Parallel()

	p := &Parser{}
	// lowercase 'm' indicates release
	events, _ := feedAll(t, p, []byte("\033[<0;10;5m"))

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Action != Release {
		t.Errorf("action: got %d, want Release", events[0].Action)
	}
}

func TestParseScrollUp(t *testing.T) {
	t.Parallel()

	p := &Parser{}
	events, _ := feedAll(t, p, []byte("\033[<64;15;8M"))

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Button != ScrollUp {
		t.Errorf("button: got %d, want ScrollUp", events[0].Button)
	}
}

func TestParseScrollDown(t *testing.T) {
	t.Parallel()

	p := &Parser{}
	events, _ := feedAll(t, p, []byte("\033[<65;15;8M"))

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Button != ScrollDown {
		t.Errorf("button: got %d, want ScrollDown", events[0].Button)
	}
}

func TestParseMotion(t *testing.T) {
	t.Parallel()

	p := &Parser{}
	// Button 0 + 32 (motion flag) = 32
	events, _ := feedAll(t, p, []byte("\033[<32;10;5M"))

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Action != Motion {
		t.Errorf("action: got %d, want Motion", events[0].Action)
	}
	if events[0].Button != ButtonLeft {
		t.Errorf("button: got %d, want ButtonLeft", events[0].Button)
	}
}

func TestInputLooksLikeMouse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  []byte
		want bool
	}{
		{name: "full sgr mouse press", raw: []byte("\033[<0;10;5M"), want: true},
		{name: "partial sgr mouse prefix", raw: []byte("\033[<0;10;5"), want: true},
		{name: "escape key", raw: []byte{0x1b}, want: false},
		{name: "arrow key", raw: []byte("\033[A"), want: false},
		{name: "plain text", raw: []byte("a"), want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var p Parser
			if got := p.InputLooksLikeMouse(tt.raw); got != tt.want {
				t.Fatalf("InputLooksLikeMouse(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestInputLooksLikeMouseFromInProgressParser(t *testing.T) {
	t.Parallel()

	var p Parser
	if _, ok, flushed := p.Feed(0x1b); ok || len(flushed) != 0 {
		t.Fatalf("first byte should start escape buffering, got ok=%v flushed=%q", ok, flushed)
	}
	if _, ok, flushed := p.Feed('['); ok || len(flushed) != 0 {
		t.Fatalf("second byte should keep buffering, got ok=%v flushed=%q", ok, flushed)
	}
	if _, ok, flushed := p.Feed('<'); ok || len(flushed) != 0 {
		t.Fatalf("third byte should mark mouse candidate, got ok=%v flushed=%q", ok, flushed)
	}
	if !p.InputLooksLikeMouse([]byte("0;10;5M")) {
		t.Fatal("continuation bytes should still be recognized as mouse input")
	}
}

func TestParseModifiers(t *testing.T) {
	t.Parallel()

	p := &Parser{}
	// Shift(4) + Alt(8) + Ctrl(16) = 28, button 0 → Cb = 28
	events, _ := feedAll(t, p, []byte("\033[<28;1;1M"))

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if !ev.Shift || !ev.Alt || !ev.Ctrl {
		t.Errorf("modifiers: shift=%v alt=%v ctrl=%v, want all true", ev.Shift, ev.Alt, ev.Ctrl)
	}
}

func TestNonMouseEscapeFlushes(t *testing.T) {
	t.Parallel()

	p := &Parser{}
	// A regular escape sequence like \033[A (cursor up)
	events, flushed := feedAll(t, p, []byte("\033[A"))

	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
	if string(flushed) != "\033[A" {
		t.Errorf("flushed: got %q, want %q", flushed, "\033[A")
	}
}

func TestNonMouseCSIUFlushesAsSingleSequence(t *testing.T) {
	t.Parallel()

	p := &Parser{}

	events, flushed := feedAll(t, p, []byte("\033[97;5u"))

	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
	if string(flushed) != "\033[97;5u" {
		t.Errorf("flushed: got %q, want %q", flushed, "\033[97;5u")
	}
}

func TestNormalInputFlushes(t *testing.T) {
	t.Parallel()

	p := &Parser{}
	events, flushed := feedAll(t, p, []byte("hello"))

	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
	if string(flushed) != "hello" {
		t.Errorf("flushed: got %q, want %q", flushed, "hello")
	}
}

func TestMixedInputAndMouse(t *testing.T) {
	t.Parallel()

	p := &Parser{}
	// "abc" + mouse click + "def"
	input := []byte("abc\033[<0;5;3Mdef")
	events, flushed := feedAll(t, p, input)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Button != ButtonLeft {
		t.Errorf("button: got %d, want ButtonLeft", events[0].Button)
	}
	if string(flushed) != "abcdef" {
		t.Errorf("flushed: got %q, want %q", flushed, "abcdef")
	}
}

func TestMultipleMouseEvents(t *testing.T) {
	t.Parallel()

	p := &Parser{}
	// Press then release
	input := []byte("\033[<0;5;3M\033[<0;5;3m")
	events, _ := feedAll(t, p, input)

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Action != Press {
		t.Errorf("event 0: got action %d, want Press", events[0].Action)
	}
	if events[1].Action != Release {
		t.Errorf("event 1: got action %d, want Release", events[1].Action)
	}
}

func TestLargeCoordinates(t *testing.T) {
	t.Parallel()

	p := &Parser{}
	// SGR mode has no coordinate limit — test column 500, row 300
	events, _ := feedAll(t, p, []byte("\033[<0;500;300M"))

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].X != 499 || events[0].Y != 299 {
		t.Errorf("position: got (%d,%d), want (499,299)", events[0].X, events[0].Y)
	}
}

func TestInProgress(t *testing.T) {
	t.Parallel()

	p := &Parser{}
	if p.InProgress() {
		t.Error("should not be in progress initially")
	}

	p.Feed(0x1b)
	if !p.InProgress() {
		t.Error("should be in progress after ESC")
	}

	p.Feed('[')
	p.Feed('<')
	p.Feed('0')
	p.Feed(';')
	p.Feed('1')
	p.Feed(';')
	p.Feed('1')
	p.Feed('M')
	if p.InProgress() {
		t.Error("should not be in progress after complete sequence")
	}
}

func TestDragDeltaTracking(t *testing.T) {
	t.Parallel()

	p := &Parser{}

	// Press at (10, 5)
	events, _ := feedAll(t, p, []byte("\033[<0;11;6M"))
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	// First event: LastX/LastY == X/Y (no previous)
	if events[0].LastX != 10 || events[0].LastY != 5 {
		t.Errorf("first event: LastX=%d LastY=%d, want (10,5)", events[0].LastX, events[0].LastY)
	}

	// Drag to (15, 5) — motion with button held (Cb = 0 + 32 = 32)
	events, _ = feedAll(t, p, []byte("\033[<32;16;6M"))
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.X != 15 || ev.Y != 5 {
		t.Errorf("drag: X=%d Y=%d, want (15,5)", ev.X, ev.Y)
	}
	if ev.LastX != 10 || ev.LastY != 5 {
		t.Errorf("drag: LastX=%d LastY=%d, want (10,5)", ev.LastX, ev.LastY)
	}
	// Delta: dx = 15-10 = 5, dy = 5-5 = 0
	dx := ev.X - ev.LastX
	dy := ev.Y - ev.LastY
	if dx != 5 || dy != 0 {
		t.Errorf("drag delta: dx=%d dy=%d, want (5,0)", dx, dy)
	}

	// Drag further to (15, 8)
	events, _ = feedAll(t, p, []byte("\033[<32;16;9M"))
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev = events[0]
	if ev.LastX != 15 || ev.LastY != 5 {
		t.Errorf("second drag: LastX=%d LastY=%d, want (15,5)", ev.LastX, ev.LastY)
	}
}

func TestButtonString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		btn  Button
		want string
	}{
		{ButtonLeft, "left"},
		{ButtonMiddle, "middle"},
		{ButtonRight, "right"},
		{ScrollUp, "scroll-up"},
		{ScrollDown, "scroll-down"},
	}
	for _, tt := range tests {
		if got := fmt.Sprint(tt.btn); got != tt.want {
			t.Errorf("Button(%d).String() = %q, want %q", tt.btn, got, tt.want)
		}
	}
}
