package mux

import (
	"bytes"
	"encoding/json"
)

// MetaUpdate is the JSON payload from an amux-meta escape sequence.
// Pointer fields distinguish "set empty" (clear) from "not set" (leave unchanged).
type MetaUpdate struct {
	Task   *string `json:"task,omitempty"`
	PR     *string `json:"pr,omitempty"`
	Branch *string `json:"branch,omitempty"`
}

var amuxMetaPrefix = []byte("\x1b]999;amux-meta;")

const maxAmuxMetaSize = 4 * 1024

// AmuxMetaScanner detects amux metadata push sequences in a stream of
// terminal output bytes. Handles sequences that span multiple reads.
//
// Format: \x1b]999;amux-meta;{json}\x07
type AmuxMetaScanner struct {
	partial []byte
}

// Scan processes a chunk of raw PTY output and returns any complete
// metadata update sequences found.
func (s *AmuxMetaScanner) Scan(data []byte) []MetaUpdate {
	var results []MetaUpdate

	if len(s.partial) > 0 {
		data = append(s.partial, data...)
		s.partial = nil
	}

	for len(data) > 0 {
		idx := bytes.Index(data, amuxMetaPrefix)
		if idx < 0 {
			break
		}

		rest := data[idx:]

		if len(rest) > maxAmuxMetaSize {
			data = data[idx+len(amuxMetaPrefix):]
			continue
		}

		end := findMetaEnd(rest)
		if end < 0 {
			s.partial = bytes.Clone(rest)
			break
		}

		jsonStart := len(amuxMetaPrefix)
		jsonData := rest[jsonStart:end]

		var update MetaUpdate
		if err := json.Unmarshal(jsonData, &update); err == nil {
			results = append(results, update)
		}

		data = data[idx+end+1:]
	}

	if s.partial == nil && len(data) > 0 {
		for i := max(0, len(data)-len(amuxMetaPrefix)+1); i < len(data); i++ {
			if bytes.HasPrefix(amuxMetaPrefix, data[i:]) {
				s.partial = bytes.Clone(data[i:])
				break
			}
		}
	}

	return results
}

func findMetaEnd(data []byte) int {
	after := data[min(len(amuxMetaPrefix), len(data)):]
	belOff := bytes.IndexByte(after, 0x07)
	if belOff < 0 {
		return -1
	}
	return len(amuxMetaPrefix) + belOff
}

// FormatMetaSequence builds the escape sequence for a metadata update.
func FormatMetaSequence(update MetaUpdate) []byte {
	jsonData, _ := json.Marshal(update)
	var buf bytes.Buffer
	buf.Write(amuxMetaPrefix)
	buf.Write(jsonData)
	buf.WriteByte(0x07)
	return buf.Bytes()
}
