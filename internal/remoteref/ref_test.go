package remoteref

import "testing"

func TestParseAndFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  Ref
	}{
		{
			name:  "pane name",
			value: "amux://hetzner-1/main/pane/name/pane-1786",
			want:  Ref{Remote: "hetzner-1", Session: "main", Kind: KindPane, SelectorKind: SelectorName, Selector: "pane-1786"},
		},
		{
			name:  "pane id",
			value: "amux://hetzner-1/main/pane/id/1786",
			want:  Ref{Remote: "hetzner-1", Session: "main", Kind: KindPane, SelectorKind: SelectorID, Selector: "1786"},
		},
		{
			name:  "window name",
			value: "amux://hetzner-1/main/window/name/main",
			want:  Ref{Remote: "hetzner-1", Session: "main", Kind: KindWindow, SelectorKind: SelectorName, Selector: "main"},
		},
		{
			name:  "window index",
			value: "amux://hetzner-1/main/window/index/2",
			want:  Ref{Remote: "hetzner-1", Session: "main", Kind: KindWindow, SelectorKind: SelectorIndex, Selector: "2"},
		},
		{
			name:  "percent encoded session and selector",
			value: "amux://hetzner-1/main%20session/pane/name/pane%2Falpha%20one",
			want:  Ref{Remote: "hetzner-1", Session: "main session", Kind: KindPane, SelectorKind: SelectorName, Selector: "pane/alpha one"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Parse(tt.value)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.value, err)
			}
			if got != tt.want {
				t.Fatalf("Parse(%q) = %+v, want %+v", tt.value, got, tt.want)
			}

			formatted, err := Format(tt.want)
			if err != nil {
				t.Fatalf("Format(%+v): %v", tt.want, err)
			}
			if formatted != tt.value {
				t.Fatalf("Format(%+v) = %q, want %q", tt.want, formatted, tt.value)
			}
		})
	}
}

func TestParseRejectsMalformedRefs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
	}{
		{name: "wrong scheme", value: "ssh://hetzner-1/main/pane/name/pane-1"},
		{name: "missing remote", value: "amux:///main/pane/name/pane-1"},
		{name: "missing session", value: "amux://hetzner-1//pane/name/pane-1"},
		{name: "missing selector", value: "amux://hetzner-1/main/pane/name"},
		{name: "extra segment", value: "amux://hetzner-1/main/pane/name/pane-1/extra"},
		{name: "unknown kind", value: "amux://hetzner-1/main/tab/name/main"},
		{name: "unknown selector kind", value: "amux://hetzner-1/main/pane/title/pane-1"},
		{name: "pane index invalid", value: "amux://hetzner-1/main/pane/index/1"},
		{name: "window id invalid", value: "amux://hetzner-1/main/window/id/1"},
		{name: "invalid id", value: "amux://hetzner-1/main/pane/id/not-a-number"},
		{name: "zero id", value: "amux://hetzner-1/main/pane/id/0"},
		{name: "invalid index", value: "amux://hetzner-1/main/window/index/not-a-number"},
		{name: "zero index", value: "amux://hetzner-1/main/window/index/0"},
		{name: "bad escape", value: "amux://hetzner-1/main/pane/name/pane%ZZ"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := Parse(tt.value); err == nil {
				t.Fatalf("Parse(%q) error = nil, want non-nil", tt.value)
			}
		})
	}
}

func TestFormatRejectsInvalidRefs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  Ref
	}{
		{name: "missing remote", ref: Ref{Session: "main", Kind: KindPane, SelectorKind: SelectorName, Selector: "pane-1"}},
		{name: "missing session", ref: Ref{Remote: "hetzner-1", Kind: KindPane, SelectorKind: SelectorName, Selector: "pane-1"}},
		{name: "wrong selector combination", ref: Ref{Remote: "hetzner-1", Session: "main", Kind: KindWindow, SelectorKind: SelectorID, Selector: "1"}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := Format(tt.ref); err == nil {
				t.Fatalf("Format(%+v) error = nil, want non-nil", tt.ref)
			}
		})
	}
}
