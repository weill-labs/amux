package mux

import (
	"testing"
)

func TestOSC52ScanBEL(t *testing.T) {
	t.Parallel()

	s := &OSC52Scanner{}
	data := []byte("before\x1b]52;c;SGVsbG8=\x07after")
	results := s.Scan(data)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	want := "\x1b]52;c;SGVsbG8=\x07"
	if string(results[0]) != want {
		t.Errorf("got %q, want %q", results[0], want)
	}
}

func TestOSC52ScanST(t *testing.T) {
	t.Parallel()

	s := &OSC52Scanner{}
	data := []byte("before\x1b]52;c;SGVsbG8=\x1b\\after")
	results := s.Scan(data)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	want := "\x1b]52;c;SGVsbG8=\x1b\\"
	if string(results[0]) != want {
		t.Errorf("got %q, want %q", results[0], want)
	}
}

func TestOSC52ScanNoSequence(t *testing.T) {
	t.Parallel()

	s := &OSC52Scanner{}
	data := []byte("normal terminal output \x1b[32mgreen\x1b[0m")
	results := s.Scan(data)

	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestOSC52ScanMultiple(t *testing.T) {
	t.Parallel()

	s := &OSC52Scanner{}
	data := []byte("\x1b]52;c;Zmlyc3Q=\x07text\x1b]52;c;c2Vjb25k\x07")
	results := s.Scan(data)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if string(results[0]) != "\x1b]52;c;Zmlyc3Q=\x07" {
		t.Errorf("first: got %q", results[0])
	}
	if string(results[1]) != "\x1b]52;c;c2Vjb25k\x07" {
		t.Errorf("second: got %q", results[1])
	}
}

func TestOSC52ScanPartialAcrossReads(t *testing.T) {
	t.Parallel()

	s := &OSC52Scanner{}

	// First read: partial OSC 52 (no terminator)
	results := s.Scan([]byte("pre\x1b]52;c;SGVs"))
	if len(results) != 0 {
		t.Fatalf("expected 0 results from partial, got %d", len(results))
	}

	// Second read: rest of the sequence
	results = s.Scan([]byte("bG8=\x07post"))
	if len(results) != 1 {
		t.Fatalf("expected 1 result after completing sequence, got %d", len(results))
	}
	want := "\x1b]52;c;SGVsbG8=\x07"
	if string(results[0]) != want {
		t.Errorf("got %q, want %q", results[0], want)
	}
}

func TestOSC52ScanEmpty(t *testing.T) {
	t.Parallel()

	s := &OSC52Scanner{}
	results := s.Scan(nil)
	if len(results) != 0 {
		t.Errorf("expected 0 results for nil data, got %d", len(results))
	}
	results = s.Scan([]byte{})
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty data, got %d", len(results))
	}
}

func TestOSC52ScanPartialPrefix(t *testing.T) {
	t.Parallel()

	s := &OSC52Scanner{}

	// First read ends mid-prefix: "\x1b]5" is the start of "\x1b]52;"
	results := s.Scan([]byte("normal output\x1b]5"))
	if len(results) != 0 {
		t.Fatalf("expected 0 results from partial prefix, got %d", len(results))
	}

	// Second read completes the prefix and the full sequence
	results = s.Scan([]byte("2;c;SGVsbG8=\x07"))
	if len(results) != 1 {
		t.Fatalf("expected 1 result after completing split prefix, got %d", len(results))
	}
	want := "\x1b]52;c;SGVsbG8=\x07"
	if string(results[0]) != want {
		t.Errorf("got %q, want %q", results[0], want)
	}
}

func TestOSC52ScanMaxSizeGuard(t *testing.T) {
	t.Parallel()

	s := &OSC52Scanner{}

	// Build a sequence that exceeds maxOSC52Size (prefix + >4MB of data, no terminator)
	huge := make([]byte, 0, maxOSC52Size+100)
	huge = append(huge, osc52Prefix...)
	huge = append(huge, make([]byte, maxOSC52Size+100-len(osc52Prefix))...)
	// No terminator — scanner should abandon, not buffer

	results := s.Scan(huge)
	if len(results) != 0 {
		t.Fatalf("expected 0 results for oversized sequence, got %d", len(results))
	}
	if len(s.partial) > 0 {
		t.Errorf("scanner should not buffer oversized sequences, but partial has %d bytes", len(s.partial))
	}
}

func TestOSC52ScanPrimarySelection(t *testing.T) {
	t.Parallel()

	s := &OSC52Scanner{}
	// Test with primary selection "p" instead of clipboard "c"
	data := []byte("\x1b]52;p;dGVzdA==\x07")
	results := s.Scan(data)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if string(results[0]) != "\x1b]52;p;dGVzdA==\x07" {
		t.Errorf("got %q", results[0])
	}
}
