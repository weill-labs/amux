package wrap

import "bytes"

// enterAlt is the ANSI escape sequence for entering alternate screen mode.
var enterAlt = []byte("\x1b[?1049h")

// exitAlt is the ANSI escape sequence for leaving alternate screen mode.
var exitAlt = []byte("\x1b[?1049l")

// maxSeqLen is the length of the longest sequence we scan for.
// Used to size the suffix buffer for cross-boundary detection.
var maxSeqLen = len(enterAlt) // both sequences are the same length

// Parser scans a byte stream for alt-screen enter/exit sequences.
// It does NOT modify the data — it only tracks state.
type Parser struct {
	altScreen bool
	suffix    []byte // last (maxSeqLen-1) bytes from previous Feed
}

// Feed scans data for alt-screen transitions. It returns data unmodified.
// Sequences split across consecutive Feed calls are detected via a suffix buffer.
func (p *Parser) Feed(data []byte) []byte {
	if len(data) == 0 {
		return data
	}

	// Join suffix from previous call with current data to catch split sequences.
	search := data
	if len(p.suffix) > 0 {
		search = append(p.suffix, data...)
	}

	// Scan for all occurrences of enter/exit in the combined buffer.
	// Process them in order to track the final state correctly.
	pos := 0
	for pos < len(search) {
		enterIdx := bytes.Index(search[pos:], enterAlt)
		exitIdx := bytes.Index(search[pos:], exitAlt)

		if enterIdx < 0 && exitIdx < 0 {
			break
		}

		// Pick whichever comes first
		if enterIdx >= 0 && (exitIdx < 0 || enterIdx <= exitIdx) {
			p.altScreen = true
			pos += enterIdx + len(enterAlt)
		} else {
			p.altScreen = false
			pos += exitIdx + len(exitAlt)
		}
	}

	// Save suffix from the combined search buffer so sequences split across
	// 3+ Feed calls are still detected.
	suffixLen := min(maxSeqLen-1, len(search))
	p.suffix = make([]byte, suffixLen)
	copy(p.suffix, search[len(search)-suffixLen:])

	return data
}

// InAltScreen returns true if the child is currently in alternate screen mode.
func (p *Parser) InAltScreen() bool {
	return p.altScreen
}
