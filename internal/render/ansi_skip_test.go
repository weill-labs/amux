package render

import "testing"

func TestSkipANSISequenceStopsOSCBeforeFollowingCSI(t *testing.T) {
	t.Parallel()

	input := "\x1b]11;ff/00/ff\x1b[1;2;3m"
	if got, want := skipANSISequence(input, 0), len("\x1b]11;ff/00/ff"); got != want {
		t.Fatalf("skipANSISequence() = %d, want %d", got, want)
	}
}

func TestCSIParamsPreservesCollectedParamsForTruncatedCSI(t *testing.T) {
	t.Parallel()

	params, finalByte, end := CSIParams("\x1b[1;2;3", 2)
	if params != "1;2" {
		t.Fatalf("CSIParams() params = %q, want %q", params, "1;2")
	}
	if finalByte != 0 {
		t.Fatalf("CSIParams() finalByte = %q, want 0", finalByte)
	}
	if end != len("\x1b[1;2;3") {
		t.Fatalf("CSIParams() end = %d, want %d", end, len("\x1b[1;2;3"))
	}
}
