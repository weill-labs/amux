package mux

import (
	"strconv"
	"strings"
	"testing"
)

// FuzzWindowResolvePane seeds the documented pane reference forms plus their
// boundary cases: exact names, duplicate names, decimal IDs with leading
// zeroes, uint32 limits, prefix-shaped misses, whitespace, signs, embedded
// NULs, and raw non-UTF-8 bytes. Those cases keep the CI seed run focused on
// the parser's public contract while opt-in fuzzing mutates arbitrary user
// refs around them.
func FuzzWindowResolvePane(f *testing.F) {
	for _, seed := range []string{
		"",
		"1",
		"01",
		"0000000001",
		"2",
		"3",
		"4",
		"0000000004",
		"5",
		"0000000005",
		"4294967295",
		"004294967295",
		"4294967296",
		"004294967296",
		"18446744073709551615",
		"-1",
		"+1",
		"1\n",
		"pane-1",
		"pane-1\x00",
		"pane-2",
		"pane-",
		"pane-10",
		"shared",
		"shared\x00",
		"sh",
		" ",
		"\t1",
		string([]byte{'p', 'a', 'n', 'e', '-', 0, '1'}),
		string([]byte{0xff, '1'}),
	} {
		f.Add(seed)
	}

	w, byID, byName := resolvePaneFuzzWindow()
	f.Fuzz(func(t *testing.T, ref string) {
		got, err := w.ResolvePane(ref)
		if parsed, parseErr := strconv.ParseUint(ref, 10, 32); parseErr == nil {
			want := byID[uint32(parsed)]
			if want == nil {
				assertResolvePaneCleanError(t, got, err, "not found")
				return
			}
			if err != nil || got != want {
				t.Fatalf("ResolvePane(%q) = (%v, %v), want pane %d with nil error", ref, got, err, want.ID)
			}
			return
		}

		matches := byName[ref]
		switch len(matches) {
		case 0:
			assertResolvePaneCleanError(t, got, err, "not found")
		case 1:
			if err != nil || got != matches[0] {
				t.Fatalf("ResolvePane(%q) = (%v, %v), want pane %d with nil error", ref, got, err, matches[0].ID)
			}
		default:
			assertResolvePaneCleanError(t, got, err, "ambiguous")
		}
	})
}

func resolvePaneFuzzWindow() (*Window, map[uint32]*Pane, map[string][]*Pane) {
	panes := []*Pane{
		{ID: 1, Meta: PaneMeta{Name: "pane-1"}},
		{ID: 2, Meta: PaneMeta{Name: "pane-2"}},
		{ID: 3, Meta: PaneMeta{Name: "shared"}},
		{ID: 4, Meta: PaneMeta{Name: "shared"}},
	}

	w := NewWindow(panes[0], 120, 40)
	for _, pane := range panes[1:] {
		if _, err := w.SplitRoot(SplitVertical, pane); err != nil {
			panic(err)
		}
	}

	byID := make(map[uint32]*Pane, len(panes))
	byName := make(map[string][]*Pane, len(panes))
	for _, pane := range panes {
		byID[pane.ID] = pane
		byName[pane.Meta.Name] = append(byName[pane.Meta.Name], pane)
	}
	return w, byID, byName
}

func assertResolvePaneCleanError(t *testing.T, got *Pane, err error, wantReason string) {
	t.Helper()
	if err == nil {
		t.Fatalf("ResolvePane returned pane %v with nil error, want %s error", got, wantReason)
	}
	if got != nil {
		t.Fatalf("ResolvePane returned pane %v with error %v, want nil pane on error", got, err)
	}
	if !strings.Contains(err.Error(), wantReason) {
		t.Fatalf("ResolvePane error = %q, want reason containing %q", err.Error(), wantReason)
	}
}
