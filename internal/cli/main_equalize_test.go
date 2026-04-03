package cli

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseEqualizeArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    []string
		wantErr string
	}{
		{name: "default horizontal", args: nil, want: nil},
		{name: "vertical", args: []string{"--vertical"}, want: []string{"--vertical"}},
		{name: "all", args: []string{"--all"}, want: []string{"--all"}},
		{name: "duplicate vertical", args: []string{"--vertical", "--vertical"}, want: []string{"--vertical"}},
		{name: "conflicting modes", args: []string{"--vertical", "--all"}, wantErr: "conflicting equalize modes"},
		{name: "unknown arg", args: []string{"pane-1"}, wantErr: `unknown equalize arg "pane-1"`},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseEqualizeArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ParseEqualizeArgs(%v): expected error containing %q", tt.args, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseEqualizeArgs(%v): error = %q, want substring %q", tt.args, err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseEqualizeArgs(%v): unexpected error: %v", tt.args, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseEqualizeArgs(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}
