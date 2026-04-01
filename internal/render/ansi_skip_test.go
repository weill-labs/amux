package render

import "testing"

func TestDecodeANSISequenceRejectsOutOfBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		i    int
	}{
		{name: "negative index", i: -1},
		{name: "past end", i: len("x")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd, params, n := decodeANSISequence("x", tt.i)
			if cmd != 0 || params != nil || n != 0 {
				t.Fatalf("decodeANSISequence() = (%v, %v, %d), want zero values", cmd, params, n)
			}
		})
	}
}

func TestSkipANSISequenceLeavesPlainTextIndexUnchanged(t *testing.T) {
	t.Parallel()

	if got := skipANSISequence("plain", 1); got != 1 {
		t.Fatalf("skipANSISequence() = %d, want %d", got, 1)
	}
}

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

func TestCSIParamsRejectsNonCSIStart(t *testing.T) {
	t.Parallel()

	params, finalByte, end := CSIParams("plain", 0)
	if params != "" || finalByte != 0 || end != 0 {
		t.Fatalf("CSIParams() = (%q, %q, %d), want (\"\", 0, 0)", params, finalByte, end)
	}
}

func TestCSIParamsReconstructsPrefixIntermediateAndSubparams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantParam string
		wantFinal byte
	}{
		{name: "private prefix and intermediate", input: "\x1b[?1 q", wantParam: "?1 ", wantFinal: 'q'},
		{name: "subparams", input: "\x1b[4:3m", wantParam: "4:3", wantFinal: 'm'},
		{name: "missing leading param", input: "\x1b[;5H", wantParam: ";5", wantFinal: 'H'},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			params, finalByte, end := CSIParams(tt.input, 2)
			if params != tt.wantParam {
				t.Fatalf("CSIParams() params = %q, want %q", params, tt.wantParam)
			}
			if finalByte != tt.wantFinal {
				t.Fatalf("CSIParams() finalByte = %q, want %q", finalByte, tt.wantFinal)
			}
			if end != len(tt.input) {
				t.Fatalf("CSIParams() end = %d, want %d", end, len(tt.input))
			}
		})
	}
}
