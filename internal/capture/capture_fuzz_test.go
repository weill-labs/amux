package capture

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// FuzzParseArgs seeds capture's flag parser with the supported flags, malformed
// flag values, repeated --rewrap forms, conflicting output modes, unknown
// positionals, empty argv elements, and raw non-UTF-8 bytes. These seeds keep
// CI focused on the loose CLI contract while opt-in fuzzing mutates arbitrary
// token sequences around them.
func FuzzParseArgs(f *testing.F) {
	for _, seed := range []string{
		"",
		"--ansi",
		"--ansi\x00pane-1",
		"--colors\x00pane-1",
		"--format\x00json\x00pane-2",
		"--format\x00xml\x00pane-2",
		"--history\x00pane-3",
		"--history\x00--rewrap\x0080\x00pane-3",
		"--history\x00--rewrap\x00wide\x00pane-3",
		"--history\x00--rewrap\x0080\x00--rewrap\x00wide\x00pane-3",
		"--history\x00--rewrap\x0080\x00--rewrap\x00pane-3",
		"--rewrap",
		"--rewrap\x0080\x00--rewrap",
		"--client",
		"--client\x00pane-1",
		"--client\x00--format\x00json",
		"--display",
		"--display\x00pane-1",
		"--ansi\x00--colors",
		"--colors\x00--format\x00json",
		"pane-1\x00pane-2",
		"--unknown\x00pane-1",
		string([]byte{'-', '-', 'r', 'e', 'w', 'r', 'a', 'p', 0xff, 0x00, '8', '0'}),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		args := captureFuzzArgs(raw)
		req := ParseArgs(args)
		assertRewrapWidthMatchesRaw(t, req)

		screenErr, historyErr := assertCaptureValidationResult(t, req)
		if screenErr == nil || historyErr == nil {
			normalized := ArgsForRequest(req)
			if reparsed := ParseArgs(normalized); !reflect.DeepEqual(reparsed, req) {
				t.Fatalf("ParseArgs(ArgsForRequest(ParseArgs(%q))) = %+v, want %+v; args=%q normalized=%q", raw, reparsed, req, args, normalized)
			}
		}
	})
}

func captureFuzzArgs(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\x00")
}

func assertRewrapWidthMatchesRaw(t *testing.T, req Request) {
	t.Helper()
	if !req.RewrapSpecified {
		if req.RewrapRaw != "" || req.RewrapWidth != 0 {
			t.Fatalf("rewrap unset but raw=%q width=%d", req.RewrapRaw, req.RewrapWidth)
		}
		return
	}
	if req.RewrapRaw == "" {
		if req.RewrapWidth != 0 {
			t.Fatalf("empty rewrap raw has width %d, want 0", req.RewrapWidth)
		}
		return
	}
	width, err := strconv.Atoi(req.RewrapRaw)
	if err != nil {
		if req.RewrapWidth != 0 {
			t.Fatalf("invalid rewrap raw %q has width %d, want 0", req.RewrapRaw, req.RewrapWidth)
		}
		return
	}
	if req.RewrapWidth != width {
		t.Fatalf("rewrap raw %q parsed width %d, want %d", req.RewrapRaw, req.RewrapWidth, width)
	}
}

func assertCaptureValidationResult(t *testing.T, req Request) (error, error) {
	t.Helper()

	screenErr := ValidateScreenRequest(req)
	if screenErr != nil && screenErr.Error() == "" {
		t.Fatal("ValidateScreenRequest returned an empty error")
	}
	if screenErr == nil {
		if req.IncludeANSI && (req.ColorMap || req.FormatJSON) {
			t.Fatalf("screen request with mutually exclusive output modes validated: %+v", req)
		}
		if req.ColorMap && req.FormatJSON {
			t.Fatalf("screen request with mutually exclusive color/json modes validated: %+v", req)
		}
		if req.DisplayMode && (req.IncludeANSI || req.ColorMap || req.FormatJSON || req.HistoryMode || req.PaneRef != "") {
			t.Fatalf("display request with other options validated: %+v", req)
		}
		if req.ClientMode && (req.IncludeANSI || req.ColorMap || req.FormatJSON || req.HistoryMode) {
			t.Fatalf("client request with incompatible options validated: %+v", req)
		}
		if req.RewrapSpecified {
			t.Fatalf("screen request with --rewrap validated without --history: %+v", req)
		}
	}

	historyErr := ValidateHistoryRequest(req)
	if historyErr != nil && historyErr.Error() == "" {
		t.Fatal("ValidateHistoryRequest returned an empty error")
	}
	if historyErr == nil {
		if !req.HistoryMode || req.PaneRef == "" {
			t.Fatalf("history request without required fields validated: %+v", req)
		}
		if req.IncludeANSI || req.ColorMap || req.DisplayMode || req.ClientMode {
			t.Fatalf("history request with mutually exclusive flags validated: %+v", req)
		}
		if req.RewrapSpecified && req.RewrapWidth <= 0 {
			t.Fatalf("history request with invalid --rewrap width validated: %+v", req)
		}
	}
	return screenErr, historyErr
}
