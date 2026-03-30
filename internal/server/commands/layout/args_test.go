package layout

import (
	"reflect"
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

func TestParseSplitArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    SplitArgs
		wantErr string
	}{
		{
			name: "defaults to horizontal",
			want: SplitArgs{Dir: mux.SplitHorizontal},
		},
		{
			name: "pane ref",
			args: []string{"pane-1"},
			want: SplitArgs{PaneRef: "pane-1", Dir: mux.SplitHorizontal},
		},
		{
			name: "pane ref with all flags",
			args: []string{"pane-1", "root", "--vertical", "--host", "dev", "--name", "worker", "--task", "build", "--color", "blue"},
			want: SplitArgs{
				PaneRef:   "pane-1",
				RootLevel: true,
				Dir:       mux.SplitVertical,
				HostName:  "dev",
				Name:      "worker",
				Task:      "build",
				Color:     "blue",
			},
		},
		{
			name: "accepts legacy vertical shorthand",
			args: []string{"v"},
			want: SplitArgs{Dir: mux.SplitVertical},
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
			if got != tt.want {
				t.Fatalf("ParseSplitArgs(%v) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}

func TestParseSpawnArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    SpawnArgs
		wantErr string
	}{
		{
			name: "parses all fields",
			args: []string{"--name", "worker-1", "--host", "dev", "--task", "build", "--color", "rosewater"},
			want: SpawnArgs{
				Meta: mux.PaneMeta{
					Name:  "worker-1",
					Host:  "dev",
					Task:  "build",
					Color: "rosewater",
				},
			},
		},
		{
			name: "defaults host to local",
			args: []string{"--name", "worker-1"},
			want: SpawnArgs{
				Meta: mux.PaneMeta{
					Name: "worker-1",
					Host: mux.DefaultHost,
				},
			},
		},
		{
			name: "allows unnamed spawn",
			args: []string{"--task", "build"},
			want: SpawnArgs{
				Meta: mux.PaneMeta{
					Host: mux.DefaultHost,
					Task: "build",
				},
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
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseSpawnArgs(%v) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}

func TestParseAddPaneArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    AddPaneArgs
		wantErr string
	}{
		{
			name: "defaults empty",
			want: AddPaneArgs{},
		},
		{
			name: "parses name and host",
			args: []string{"--name", "worker-1", "--host", "dev", "--task", "build", "--color", "blue"},
			want: AddPaneArgs{Name: "worker-1", HostName: "dev", Task: "build", Color: "blue"},
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
			if got != tt.want {
				t.Fatalf("ParseAddPaneArgs(%v) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}
