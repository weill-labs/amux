package mux

import (
	"bytes"
	"encoding/json"
	"strings"
)

// TakeoverRequest is the JSON payload emitted by a nested amux through
// the SSH PTY to signal the local amux to splice remote panes into the
// local layout.
type TakeoverRequest struct {
	Session    string         `json:"session"`
	Host       string         `json:"host"`
	UID        string         `json:"uid"`
	SSHAddress string         `json:"ssh_address,omitempty"` // server_ip:port from SSH_CONNECTION
	SSHUser    string         `json:"ssh_user,omitempty"`    // remote user for return SSH connection
	Panes      []TakeoverPane `json:"panes"`
}

// TakeoverPane describes one remote pane in a takeover request.
type TakeoverPane struct {
	ID   uint32 `json:"id"`
	Name string `json:"name"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// TakeoverAck is the legacy fixed ack (no session name). Kept for reference.
// New code should use FormatTakeoverAck/ParseTakeoverAck.
const TakeoverAck = "\x1b]999;amux-takeover-ack\x07"

// takeoverAckPrefix is the OSC prefix for a session-carrying ack.
const takeoverAckPrefix = "\x1b]999;amux-takeover-ack;"

// FormatTakeoverAck builds the ack sequence carrying the agreed remote session name.
// The remote amux reads the session name from the ack and calls runServer with it,
// ensuring both sides agree on the Unix socket path.
func FormatTakeoverAck(session string) string {
	return takeoverAckPrefix + session + "\x07"
}

// ParseTakeoverAck extracts the session name from a TakeoverAck sequence.
// Returns (session, true) if the sequence is a valid session-carrying ack,
// or ("", false) if it is not (e.g. old fixed-format ack or unrelated data).
func ParseTakeoverAck(data string) (string, bool) {
	if !strings.HasPrefix(data, takeoverAckPrefix) {
		return "", false
	}
	rest := data[len(takeoverAckPrefix):]
	belIdx := strings.IndexByte(rest, 0x07)
	if belIdx < 0 {
		return "", false
	}
	session := rest[:belIdx]
	if session == "" {
		return "", false
	}
	return session, true
}

// amuxControlPrefix is the OSC 999 prefix for amux control sequences.
var amuxControlPrefix = []byte("\x1b]999;amux-takeover;")

// maxAmuxControlSize limits buffered partial control data to prevent
// unbounded growth.
const maxAmuxControlSize = 64 * 1024 // 64 KB (JSON payloads are small)

// AmuxControlScanner detects amux takeover sequences in a stream of
// terminal output bytes. It handles sequences that span multiple reads
// by buffering partial sequences — same streaming pattern as OSC52Scanner.
//
// Format: \x1b]999;amux-takeover;<json>\x07
type AmuxControlScanner struct {
	partial []byte // buffered partial sequence from previous Scan call
}

// Scan processes a chunk of raw PTY output and returns any complete amux
// control sequences found. Returns parsed TakeoverRequest structs.
func (s *AmuxControlScanner) Scan(data []byte) []TakeoverRequest {
	var results []TakeoverRequest

	// Prepend partial data from previous read
	if len(s.partial) > 0 {
		data = append(s.partial, data...)
		s.partial = nil
	}

	for len(data) > 0 {
		idx := bytes.Index(data, amuxControlPrefix)
		if idx < 0 {
			break
		}

		rest := data[idx:]

		// Guard against unbounded buffering
		if len(rest) > maxAmuxControlSize {
			data = data[idx+len(amuxControlPrefix):]
			continue
		}

		end := findControlEnd(rest)
		if end < 0 {
			// No terminator yet — buffer the partial sequence
			s.partial = bytes.Clone(rest)
			break
		}

		// Extract JSON payload between prefix and terminator
		jsonStart := len(amuxControlPrefix)
		jsonData := rest[jsonStart:end]

		var req TakeoverRequest
		if err := json.Unmarshal(jsonData, &req); err == nil {
			results = append(results, req)
		}

		// Continue scanning after this sequence (+1 for BEL terminator)
		data = data[idx+end+1:]
	}

	// Buffer a trailing partial prefix so the next Scan call can match
	if s.partial == nil && len(data) > 0 {
		for i := max(0, len(data)-len(amuxControlPrefix)+1); i < len(data); i++ {
			if bytes.HasPrefix(amuxControlPrefix, data[i:]) {
				s.partial = bytes.Clone(data[i:])
				break
			}
		}
	}

	return results
}

// findControlEnd finds the BEL terminator (\x07) in an amux control sequence.
// Returns the offset of the terminator, or -1 if not found.
func findControlEnd(data []byte) int {
	// Search after the prefix
	after := data[min(len(amuxControlPrefix), len(data)):]
	belOff := bytes.IndexByte(after, 0x07)
	if belOff < 0 {
		return -1
	}
	return len(amuxControlPrefix) + belOff
}

// FormatTakeoverSequence builds the full escape sequence for a takeover request.
func FormatTakeoverSequence(req TakeoverRequest) []byte {
	jsonData, _ := json.Marshal(req)
	var buf bytes.Buffer
	buf.Write(amuxControlPrefix)
	buf.Write(jsonData)
	buf.WriteByte(0x07)
	return buf.Bytes()
}
