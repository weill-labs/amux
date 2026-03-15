package mux

import "bytes"

// OSC52Scanner detects OSC 52 clipboard sequences in a stream of terminal
// output bytes. It handles sequences that span multiple reads by buffering
// partial sequences.
//
// OSC 52 format: \x1b]52;<selection>;<base64-data><terminator>
//   - <selection> is typically "c" (clipboard), "p" (primary), etc.
//   - <terminator> is BEL (\x07) or ST (\x1b\\)
type OSC52Scanner struct {
	partial []byte // buffered partial OSC 52 from previous Scan call
}

var osc52Prefix = []byte("\x1b]52;")

// maxOSC52Size limits buffered partial OSC 52 data to prevent unbounded growth.
const maxOSC52Size = 4 * 1024 * 1024 // 4 MB

// Scan processes a chunk of raw PTY output and returns any complete OSC 52
// sequences found. Returns the full escape sequences including the prefix
// and terminator.
func (s *OSC52Scanner) Scan(data []byte) [][]byte {
	var results [][]byte

	// Prepend partial data from previous read
	if len(s.partial) > 0 {
		data = append(s.partial, data...)
		s.partial = nil
	}

	for len(data) > 0 {
		idx := bytes.Index(data, osc52Prefix)
		if idx < 0 {
			break
		}

		rest := data[idx:]

		// Guard against unbounded buffering
		if len(rest) > maxOSC52Size {
			// Abandon — skip past the prefix and continue
			data = data[idx+len(osc52Prefix):]
			continue
		}

		end, termLen := findOSC52End(rest)
		if end < 0 {
			// No terminator yet — buffer the partial sequence
			s.partial = bytes.Clone(rest)
			break
		}

		results = append(results, bytes.Clone(rest[:end+termLen]))

		// Continue scanning after this sequence
		data = data[idx+end+termLen:]
	}

	// Buffer a trailing partial prefix (e.g., data ends with "\x1b]5")
	// so the next Scan call can match the complete "\x1b]52;" prefix.
	if s.partial == nil && len(data) > 0 {
		for i := max(0, len(data)-len(osc52Prefix)+1); i < len(data); i++ {
			if bytes.HasPrefix(osc52Prefix, data[i:]) {
				s.partial = bytes.Clone(data[i:])
				break
			}
		}
	}

	return results
}

// findOSC52End finds the terminator in an OSC 52 sequence.
// Returns the offset of the terminator and its length, or (-1, 0) if not found.
// Recognizes BEL (\x07) and ST (\x1b\\).
func findOSC52End(data []byte) (int, int) {
	// Search after the prefix — terminators can only appear in the payload.
	after := data[min(len(osc52Prefix), len(data)):]

	belOff := bytes.IndexByte(after, 0x07)
	stOff := bytes.Index(after, []byte("\x1b\\"))

	// Pick the earliest terminator, adjusting offset to be data-relative.
	offset := len(osc52Prefix)
	switch {
	case belOff >= 0 && (stOff < 0 || belOff <= stOff):
		return offset + belOff, 1
	case stOff >= 0:
		return offset + stOff, 2
	default:
		return -1, 0
	}
}
