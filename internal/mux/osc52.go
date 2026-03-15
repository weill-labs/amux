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
			s.partial = make([]byte, len(rest))
			copy(s.partial, rest)
			break
		}

		// Extract complete OSC 52 sequence
		seq := make([]byte, end+termLen)
		copy(seq, rest[:end+termLen])
		results = append(results, seq)

		// Continue scanning after this sequence
		data = data[idx+end+termLen:]
	}

	return results
}

// findOSC52End finds the terminator in an OSC 52 sequence.
// Returns the offset of the terminator and its length, or (-1, 0) if not found.
// Recognizes BEL (\x07) and ST (\x1b\\).
func findOSC52End(data []byte) (end, termLen int) {
	// Skip the prefix when looking for terminators — the terminators
	// can only appear after the prefix.
	searchStart := len(osc52Prefix)
	if searchStart >= len(data) {
		return -1, 0
	}

	search := data[searchStart:]

	endBEL := bytes.IndexByte(search, 0x07)
	endST := bytes.Index(search, []byte("\x1b\\"))

	// Adjust offsets back to data-relative positions
	if endBEL >= 0 {
		endBEL += searchStart
	}
	if endST >= 0 {
		endST += searchStart
	}

	if endBEL >= 0 && (endST < 0 || endBEL < endST) {
		return endBEL, 1
	}
	if endST >= 0 {
		return endST, 2
	}
	return -1, 0
}
