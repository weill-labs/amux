package mux

import (
	"testing"
)

func TestAmuxControlScanComplete(t *testing.T) {
	t.Parallel()
	s := &AmuxControlScanner{}
	req := TakeoverRequest{
		Session: "default@macbook",
		Host:    "lambda-a100",
		UID:     "1000",
		Panes: []TakeoverPane{
			{ID: 1, Name: "pane-1", Cols: 80, Rows: 24},
		},
	}
	data := FormatTakeoverSequence(req)
	results := s.Scan(data)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Host != "lambda-a100" {
		t.Errorf("host = %q, want %q", results[0].Host, "lambda-a100")
	}
	if results[0].Session != "default@macbook" {
		t.Errorf("session = %q, want %q", results[0].Session, "default@macbook")
	}
	if len(results[0].Panes) != 1 {
		t.Fatalf("expected 1 pane, got %d", len(results[0].Panes))
	}
	if results[0].Panes[0].Name != "pane-1" {
		t.Errorf("pane name = %q, want %q", results[0].Panes[0].Name, "pane-1")
	}
}

func TestAmuxControlScanNoSequence(t *testing.T) {
	t.Parallel()
	s := &AmuxControlScanner{}
	data := []byte("normal terminal output \x1b[32mgreen\x1b[0m")
	results := s.Scan(data)
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestAmuxControlScanWithSurroundingData(t *testing.T) {
	t.Parallel()
	s := &AmuxControlScanner{}
	req := TakeoverRequest{Host: "remote1", Panes: []TakeoverPane{{ID: 1, Name: "p1", Cols: 80, Rows: 24}}}
	seq := FormatTakeoverSequence(req)

	data := append([]byte("shell output before "), seq...)
	data = append(data, []byte(" and after")...)

	results := s.Scan(data)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Host != "remote1" {
		t.Errorf("host = %q, want %q", results[0].Host, "remote1")
	}
}

func TestAmuxControlScanPartialAcrossReads(t *testing.T) {
	t.Parallel()
	s := &AmuxControlScanner{}
	req := TakeoverRequest{Host: "partial-host", Panes: []TakeoverPane{{ID: 1, Name: "p1", Cols: 80, Rows: 24}}}
	full := FormatTakeoverSequence(req)

	// Split at various points in the sequence
	for splitAt := 1; splitAt < len(full); splitAt++ {
		s2 := &AmuxControlScanner{}
		part1 := full[:splitAt]
		part2 := full[splitAt:]

		results := s2.Scan(part1)
		if len(results) != 0 {
			t.Errorf("splitAt=%d: expected 0 results from first chunk, got %d", splitAt, len(results))
			continue
		}

		results = s2.Scan(part2)
		if len(results) != 1 {
			t.Errorf("splitAt=%d: expected 1 result from second chunk, got %d", splitAt, len(results))
			continue
		}
		if results[0].Host != "partial-host" {
			t.Errorf("splitAt=%d: host = %q, want %q", splitAt, results[0].Host, "partial-host")
		}
	}

	// Ensure clean scanner has no leftover state
	results := s.Scan(nil)
	if len(results) != 0 {
		t.Errorf("expected 0 results for nil data, got %d", len(results))
	}
}

func TestAmuxControlScanPartialPrefix(t *testing.T) {
	t.Parallel()
	s := &AmuxControlScanner{}

	// First read ends mid-prefix: "\x1b]999" is the start of "\x1b]999;amux-takeover;"
	results := s.Scan([]byte("output\x1b]999"))
	if len(results) != 0 {
		t.Fatalf("expected 0 results from partial prefix, got %d", len(results))
	}

	// Second read completes the prefix and the full sequence
	req := TakeoverRequest{Host: "split-prefix", Panes: []TakeoverPane{{ID: 1, Name: "p1", Cols: 80, Rows: 24}}}
	full := FormatTakeoverSequence(req)
	rest := full[5:] // skip the "\x1b]999" (5 bytes) that was already sent
	results = s.Scan(rest)
	if len(results) != 1 {
		t.Fatalf("expected 1 result after completing split prefix, got %d", len(results))
	}
	if results[0].Host != "split-prefix" {
		t.Errorf("host = %q, want %q", results[0].Host, "split-prefix")
	}
}

func TestAmuxControlScanMultiplePanes(t *testing.T) {
	t.Parallel()
	s := &AmuxControlScanner{}
	req := TakeoverRequest{
		Session: "multi",
		Host:    "gpu-box",
		Panes: []TakeoverPane{
			{ID: 1, Name: "pane-1", Cols: 80, Rows: 24},
			{ID: 2, Name: "pane-2", Cols: 80, Rows: 24},
			{ID: 3, Name: "pane-3", Cols: 40, Rows: 12},
		},
	}
	data := FormatTakeoverSequence(req)
	results := s.Scan(data)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(results[0].Panes) != 3 {
		t.Fatalf("expected 3 panes, got %d", len(results[0].Panes))
	}
	if results[0].Panes[2].Cols != 40 {
		t.Errorf("pane 3 cols = %d, want 40", results[0].Panes[2].Cols)
	}
}

func TestAmuxControlScanInvalidJSON(t *testing.T) {
	t.Parallel()
	s := &AmuxControlScanner{}
	// Valid prefix and terminator but invalid JSON
	data := []byte("\x1b]999;amux-takeover;{invalid json\x07")
	results := s.Scan(data)

	if len(results) != 0 {
		t.Errorf("expected 0 results for invalid JSON, got %d", len(results))
	}
}

func TestAmuxControlScanEmpty(t *testing.T) {
	t.Parallel()
	s := &AmuxControlScanner{}
	results := s.Scan(nil)
	if len(results) != 0 {
		t.Errorf("expected 0 results for nil data, got %d", len(results))
	}
	results = s.Scan([]byte{})
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty data, got %d", len(results))
	}
}

func TestAmuxControlScanMaxSizeGuard(t *testing.T) {
	t.Parallel()
	s := &AmuxControlScanner{}

	huge := make([]byte, 0, maxAmuxControlSize+100)
	huge = append(huge, amuxControlPrefix...)
	huge = append(huge, make([]byte, maxAmuxControlSize+100-len(amuxControlPrefix))...)

	results := s.Scan(huge)
	if len(results) != 0 {
		t.Fatalf("expected 0 results for oversized sequence, got %d", len(results))
	}
	if len(s.partial) > 0 {
		t.Errorf("scanner should not buffer oversized sequences, but partial has %d bytes", len(s.partial))
	}
}

func TestFormatTakeoverSequence(t *testing.T) {
	t.Parallel()
	req := TakeoverRequest{
		Session: "test-session",
		Host:    "test-host",
		UID:     "1000",
		Panes:   []TakeoverPane{{ID: 1, Name: "pane-1", Cols: 80, Rows: 24}},
	}
	seq := FormatTakeoverSequence(req)

	// Should start with the prefix and end with BEL
	if seq[0] != 0x1b || seq[1] != ']' {
		t.Error("sequence should start with ESC ]")
	}
	if seq[len(seq)-1] != 0x07 {
		t.Errorf("sequence should end with BEL, got %02x", seq[len(seq)-1])
	}

	// Should be parseable by the scanner
	s := &AmuxControlScanner{}
	results := s.Scan(seq)
	if len(results) != 1 {
		t.Fatalf("round-trip: expected 1 result, got %d", len(results))
	}
	if results[0].Session != "test-session" {
		t.Errorf("round-trip: session = %q, want %q", results[0].Session, "test-session")
	}
}

func TestFormatAndParseTakeoverAck(t *testing.T) {
	t.Parallel()
	session := "default@localMachine"
	ack := FormatTakeoverAck(session)

	got, ok := ParseTakeoverAck(ack)
	if !ok {
		t.Fatalf("ParseTakeoverAck(%q) = _, false; want true", ack)
	}
	if got != session {
		t.Errorf("ParseTakeoverAck round-trip: got %q, want %q", got, session)
	}
}

func TestParseTakeoverAckWithTrailingNewline(t *testing.T) {
	t.Parallel()
	// The server appends \n to the ack to flush through PTY canonical-mode
	// line buffering. ParseTakeoverAck must ignore trailing data after BEL.
	session := "default@host"
	ack := FormatTakeoverAck(session) + "\n"
	got, ok := ParseTakeoverAck(ack)
	if !ok {
		t.Fatalf("ParseTakeoverAck(%q) = _, false; want true", ack)
	}
	if got != session {
		t.Errorf("got %q, want %q", got, session)
	}
}

func TestParseTakeoverAckRejectsOldFormat(t *testing.T) {
	t.Parallel()
	// Old fixed TakeoverAck constant (no session name) should parse as empty session
	oldAck := "\x1b]999;amux-takeover-ack\x07"
	_, ok := ParseTakeoverAck(oldAck)
	if ok {
		t.Error("old ack format (no session) should not parse as valid")
	}
}

func TestTakeoverRequestSSHFields(t *testing.T) {
	t.Parallel()
	// Verify SSHAddress and SSHUser are carried through JSON round-trip
	req := TakeoverRequest{
		Session:    "s1@remote",
		Host:       "remote-box",
		UID:        "1000",
		SSHAddress: "10.0.0.5:22",
		SSHUser:    "ubuntu",
		Panes:      []TakeoverPane{{ID: 1, Name: "pane-1", Cols: 80, Rows: 24}},
	}
	seq := FormatTakeoverSequence(req)
	scanner := &AmuxControlScanner{}
	results := scanner.Scan(seq)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	got := results[0]
	if got.SSHAddress != "10.0.0.5:22" {
		t.Errorf("SSHAddress = %q, want %q", got.SSHAddress, "10.0.0.5:22")
	}
	if got.SSHUser != "ubuntu" {
		t.Errorf("SSHUser = %q, want %q", got.SSHUser, "ubuntu")
	}
}

func TestAmuxControlScanDoesNotMatchOSC52(t *testing.T) {
	t.Parallel()
	s := &AmuxControlScanner{}
	// OSC 52 clipboard sequence should not match
	data := []byte("\x1b]52;c;SGVsbG8=\x07")
	results := s.Scan(data)
	if len(results) != 0 {
		t.Errorf("OSC 52 should not match amux control scanner, got %d results", len(results))
	}
}
