package layout

import (
	"reflect"
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

func assertStructFields(t *testing.T, got any, want map[string]any) {
	t.Helper()

	value := reflect.ValueOf(got)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	for field, wantValue := range want {
		gotField := value.FieldByName(field)
		if !gotField.IsValid() {
			t.Fatalf("%T is missing field %q", got, field)
		}
		if gotValue := gotField.Interface(); !reflect.DeepEqual(gotValue, wantValue) {
			t.Fatalf("%T field %s = %#v, want %#v", got, field, gotValue, wantValue)
		}
	}
}

func TestParseSplitArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    map[string]any
		wantErr string
	}{
		{
			name: "defaults to horizontal",
			want: map[string]any{"Dir": mux.SplitHorizontal, "NoFocus": false},
		},
		{
			name: "pane ref",
			args: []string{"pane-1"},
			want: map[string]any{"PaneRef": "pane-1", "Dir": mux.SplitHorizontal, "NoFocus": false},
		},
		{
			name: "pane ref with all flags",
			args: []string{"pane-1", "root", "--vertical", "--host", "dev", "--name", "worker", "--task", "build", "--color", "blue", "--no-focus"},
			want: map[string]any{
				"PaneRef":   "pane-1",
				"RootLevel": true,
				"Dir":       mux.SplitVertical,
				"HostName":  "dev",
				"Name":      "worker",
				"Task":      "build",
				"Color":     "blue",
				"NoFocus":   true,
			},
		},
		{
			name: "accepts legacy vertical shorthand",
			args: []string{"v"},
			want: map[string]any{"Dir": mux.SplitVertical, "NoFocus": false},
		},
		{
			name:    "rejects legacy pane flag",
			args:    []string{"--pane", "pane-1"},
			wantErr: `unknown split arg "--pane"`,
		},
		{
			name:    "rejects legacy background flag",
			args:    []string{"pane-1", "--background"},
			wantErr: `unknown split arg "--background"`,
		},
		{
			name:    "rejects conflicting directions",
			args:    []string{"--vertical", "--horizontal"},
			wantErr: "conflicting split directions",
		},
		{
			name:    "rejects missing host value",
			args:    []string{"--host"},
			wantErr: "--host requires a value",
		},
		{
			name:    "rejects unknown arg",
			args:    []string{"--bogus"},
			wantErr: `unknown split arg "--bogus"`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseSplitArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("ParseSplitArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSplitArgs(%v): %v", tt.args, err)
			}
			assertStructFields(t, got, tt.want)
		})
	}
}

func TestParseSpawnArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    map[string]any
		wantErr string
	}{
		{
			name: "parses all fields",
			args: []string{"--name", "worker-1", "--host", "dev", "--task", "build", "--color", "rosewater", "--no-focus"},
			want: map[string]any{
				"Meta": mux.PaneMeta{
					Name:  "worker-1",
					Host:  "dev",
					Task:  "build",
					Color: "rosewater",
				},
				"NoFocus": true,
			},
		},
		{
			name: "defaults host to local",
			args: []string{"--name", "worker-1"},
			want: map[string]any{
				"Meta": mux.PaneMeta{
					Name: "worker-1",
					Host: mux.DefaultHost,
				},
				"NoFocus": false,
			},
		},
		{
			name: "allows unnamed spawn",
			args: []string{"--task", "build"},
			want: map[string]any{
				"Meta": mux.PaneMeta{
					Host: mux.DefaultHost,
					Task: "build",
				},
				"NoFocus": false,
			},
		},
		{
			name:    "rejects missing color value",
			args:    []string{"--name", "worker-1", "--color"},
			wantErr: "--color requires a value",
		},
		{
			name:    "rejects legacy background flag",
			args:    []string{"--name", "worker-1", "--background"},
			wantErr: `unknown spawn arg "--background"`,
		},
		{
			name:    "rejects unknown arg",
			args:    []string{"--name", "worker-1", "--bogus"},
			wantErr: `unknown spawn arg "--bogus"`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseSpawnArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("ParseSpawnArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSpawnArgs(%v): %v", tt.args, err)
			}
			assertStructFields(t, got, tt.want)
		})
	}
}

func TestParseAddPaneArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    map[string]any
		wantErr string
	}{
		{
			name: "defaults empty",
			want: map[string]any{"HostName": "", "Name": "", "Task": "", "Color": "", "NoFocus": false},
		},
		{
			name: "parses name and host",
			args: []string{"--name", "worker-1", "--host", "dev", "--task", "build", "--color", "blue", "--no-focus"},
			want: map[string]any{"Name": "worker-1", "HostName": "dev", "Task": "build", "Color": "blue", "NoFocus": true},
		},
		{
			name:    "rejects missing name value",
			args:    []string{"--name"},
			wantErr: "--name requires a value",
		},
		{
			name:    "rejects missing host value",
			args:    []string{"--host"},
			wantErr: "--host requires a value",
		},
		{
			name:    "rejects unknown arg",
			args:    []string{"--bogus"},
			wantErr: `unknown add-pane arg "--bogus"`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseAddPaneArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("ParseAddPaneArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseAddPaneArgs(%v): %v", tt.args, err)
			}
			assertStructFields(t, got, tt.want)
		})
	}
}
