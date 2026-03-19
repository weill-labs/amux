// Package mouse parses SGR-encoded mouse escape sequences from terminal input.
//
// When SGR extended mode is enabled (\033[?1006h), terminals report mouse
// events as:
//
//	\033[<Cb;Cx;CyM   (press / motion)
//	\033[<Cb;Cx;Cym   (release)
//
// Cb encodes the button and modifier state. Cx and Cy are 1-based coordinates.
package mouse

import "strconv"

// Button identifies which mouse button triggered the event.
type Button uint8

const (
	ButtonLeft   Button = 0
	ButtonMiddle Button = 1
	ButtonRight  Button = 2
	ButtonNone   Button = 3 // motion with no button (any-event tracking)
	ScrollUp     Button = 4
	ScrollDown   Button = 5
	ScrollLeft   Button = 6
	ScrollRight  Button = 7
)

// String returns a human-readable button name.
func (b Button) String() string {
	switch b {
	case ButtonLeft:
		return "left"
	case ButtonMiddle:
		return "middle"
	case ButtonRight:
		return "right"
	case ButtonNone:
		return "none"
	case ScrollUp:
		return "scroll-up"
	case ScrollDown:
		return "scroll-down"
	case ScrollLeft:
		return "scroll-left"
	case ScrollRight:
		return "scroll-right"
	default:
		return "unknown"
	}
}

// Action distinguishes press, release, and motion events.
type Action uint8

const (
	Press Action = iota
	Release
	Motion
)

// Event is a parsed mouse event with terminal coordinates.
type Event struct {
	Button       Button
	Action       Action
	X, Y         int // 0-based terminal coordinates (converted from 1-based SGR)
	LastX, LastY int // previous event position (for drag delta calculation)
	Shift        bool
	Alt          bool
	Ctrl         bool
}

// Parser accumulates bytes from a terminal input stream and extracts
// SGR mouse sequences. Call Feed for each byte; it returns a parsed
// Event when a complete sequence is recognized.
//
// The parser tracks the previous event position so drag handlers can
// compute deltas without external state (following tmux's pattern).
type Parser struct {
	buf     []byte
	state   parseState
	lastX   int
	lastY   int
	hasLast bool
}

type parseState int

const (
	stateNone    parseState = iota
	stateEsc                // saw \033
	stateBracket            // saw \033[
	stateLt                 // saw \033[<
	stateParams             // accumulating digits and semicolons
)

// Feed processes one byte of terminal input. Returns:
//   - (event, true, nil)  — a complete mouse event was parsed
//   - (Event{}, false, nil) — byte consumed, sequence still accumulating
//   - (Event{}, false, buffered) — not a mouse sequence; buffered contains
//     the accumulated bytes that should be handled as normal input
func (p *Parser) Feed(b byte) (Event, bool, []byte) {
	switch p.state {
	case stateNone:
		if b == 0x1b {
			p.state = stateEsc
			p.buf = append(p.buf[:0], b)
			return Event{}, false, nil
		}
		return Event{}, false, []byte{b}

	case stateEsc:
		if b == '[' {
			p.state = stateBracket
			p.buf = append(p.buf, b)
			return Event{}, false, nil
		}
		// Not a CSI sequence — flush accumulated bytes + this one
		saved := p.flush(b)
		return Event{}, false, saved

	case stateBracket:
		if b == '<' {
			p.state = stateLt
			p.buf = append(p.buf, b)
			return Event{}, false, nil
		}
		// CSI but not mouse — flush
		saved := p.flush(b)
		return Event{}, false, saved

	case stateLt:
		p.state = stateParams
		p.buf = append(p.buf, b)
		return Event{}, false, nil

	case stateParams:
		if b == 'M' || b == 'm' {
			ev, ok := parseParams(p.buf[3:], b == 'm') // skip \033[<
			p.state = stateNone
			p.buf = p.buf[:0]
			if ok {
				if p.hasLast {
					ev.LastX = p.lastX
					ev.LastY = p.lastY
				} else {
					ev.LastX = ev.X
					ev.LastY = ev.Y
				}
				p.lastX = ev.X
				p.lastY = ev.Y
				p.hasLast = true
				return ev, true, nil
			}
			// Malformed — drop the sequence
			return Event{}, false, nil
		}
		if (b >= '0' && b <= '9') || b == ';' {
			p.buf = append(p.buf, b)
			if len(p.buf) > 32 {
				// Runaway sequence — flush
				saved := p.flush(0)
				return Event{}, false, saved
			}
			return Event{}, false, nil
		}
		// Unexpected byte — flush
		saved := p.flush(b)
		return Event{}, false, saved
	}

	return Event{}, false, []byte{b}
}

// InProgress returns true if the parser is mid-sequence.
func (p *Parser) InProgress() bool {
	return p.state != stateNone
}

// FlushPending resets the parser and returns any buffered bytes from an
// incomplete non-mouse sequence candidate (for example, a lone Escape).
func (p *Parser) FlushPending() []byte {
	if p.state == stateNone || len(p.buf) == 0 {
		return nil
	}
	return p.flush(0)
}

// flush resets the parser and returns accumulated bytes plus the extra byte.
func (p *Parser) flush(extra byte) []byte {
	out := make([]byte, len(p.buf))
	copy(out, p.buf)
	if extra != 0 {
		out = append(out, extra)
	}
	p.state = stateNone
	p.buf = p.buf[:0]
	return out
}

// parseParams decodes "Cb;Cx;Cy" into an Event.
func parseParams(paramBytes []byte, isRelease bool) (Event, bool) {
	parts := splitSemicolons(paramBytes)
	if len(parts) != 3 {
		return Event{}, false
	}

	cb, err1 := strconv.Atoi(string(parts[0]))
	cx, err2 := strconv.Atoi(string(parts[1]))
	cy, err3 := strconv.Atoi(string(parts[2]))
	if err1 != nil || err2 != nil || err3 != nil {
		return Event{}, false
	}

	ev := Event{
		X:     cx - 1, // convert to 0-based
		Y:     cy - 1,
		Shift: cb&4 != 0,
		Alt:   cb&8 != 0,
		Ctrl:  cb&16 != 0,
	}

	if isRelease {
		ev.Action = Release
	} else if cb&32 != 0 {
		ev.Action = Motion
	} else {
		ev.Action = Press
	}

	// Extract button from low bits
	btnBits := cb &^ (4 | 8 | 16 | 32) // clear modifier + motion bits
	switch btnBits {
	case 0:
		ev.Button = ButtonLeft
	case 1:
		ev.Button = ButtonMiddle
	case 2:
		ev.Button = ButtonRight
	case 3:
		ev.Button = ButtonNone
	case 64:
		ev.Button = ScrollUp
	case 65:
		ev.Button = ScrollDown
	case 66:
		ev.Button = ScrollLeft
	case 67:
		ev.Button = ScrollRight
	default:
		ev.Button = ButtonNone
	}

	return ev, true
}

// splitSemicolons splits byte slice on ';' without allocating strings.
func splitSemicolons(b []byte) [][]byte {
	var parts [][]byte
	start := 0
	for i, c := range b {
		if c == ';' {
			parts = append(parts, b[start:i])
			start = i + 1
		}
	}
	parts = append(parts, b[start:])
	return parts
}
