package proto

import "testing"

func TestParsePaneRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    PaneRef
		wantErr string
	}{
		{name: "empty", raw: "", want: PaneRef{}},
		{name: "local pane", raw: "pane-1", want: PaneRef{Pane: "pane-1"}},
		{name: "host pane", raw: "builder/pane-9", want: PaneRef{Host: "builder", Pane: "pane-9"}},
		{name: "missing host", raw: "/pane-1", wantErr: `invalid pane ref "/pane-1": missing host`},
		{name: "missing pane", raw: "builder/", wantErr: `invalid pane ref "builder/": missing pane`},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParsePaneRef(tt.raw)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ParsePaneRef(%q) error = nil, want %q", tt.raw, tt.wantErr)
				}
				if got != (PaneRef{}) {
					t.Fatalf("ParsePaneRef(%q) = %#v, want zero value on error", tt.raw, got)
				}
				if err.Error() != tt.wantErr {
					t.Fatalf("ParsePaneRef(%q) error = %q, want %q", tt.raw, err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("ParsePaneRef(%q) error = %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("ParsePaneRef(%q) = %#v, want %#v", tt.raw, got, tt.want)
			}
		})
	}
}
