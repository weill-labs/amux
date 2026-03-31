package layout

import (
	"reflect"
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

func TestParseCreatePaneArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mode    createPaneMode
		args    []string
		want    createPaneArgs
		wantErr string
	}{
		{
			name: "split keeps pane ref and direction",
			mode: createPaneModeSplit,
			args: []string{"pane-1", "root", "--vertical", "--host", "dev", "--name", "worker", "--task", "build", "--color", "blue"},
			want: createPaneArgs{
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
			name: "spawn uses local vertical defaults",
			mode: createPaneModeSpawn,
			args: []string{"--name", "worker", "--task", "build"},
			want: createPaneArgs{
				Dir:  mux.SplitVertical,
				Name: "worker",
				Task: "build",
			},
		},
		{
			name:    "spawn rejects split-only pane refs",
			mode:    createPaneModeSpawn,
			args:    []string{"pane-1"},
			wantErr: `unknown spawn arg "pane-1"`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseCreatePaneArgs(tt.mode, tt.args)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseCreatePaneArgs(%v, %v) error = %v, want %q", tt.mode, tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCreatePaneArgs(%v, %v): %v", tt.mode, tt.args, err)
			}
			if got != tt.want {
				t.Fatalf("parseCreatePaneArgs(%v, %v) = %+v, want %+v", tt.mode, tt.args, got, tt.want)
			}
		})
	}
}

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
			args: []string{"pane-1", "root", "--vertical", "--host", "dev", "--name", "worker", "--task", "build", "--color", "blue", "--focus"},
			want: SplitArgs{
				PaneRef:   "pane-1",
				RootLevel: true,
				Dir:       mux.SplitVertical,
				HostName:  "dev",
				Name:      "worker",
				Task:      "build",
				Color:     "blue",
				Focus:     true,
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
			wantErr: "unknown flag: --pane",
		},
		{
			name:    "rejects legacy background flag",
			args:    []string{"pane-1", "--background"},
			wantErr: "unknown flag: --background",
		},
		{
			name:    "rejects conflicting directions",
			args:    []string{"--vertical", "--horizontal"},
			wantErr: "conflicting split directions",
		},
		{
			name:    "rejects missing host value",
			args:    []string{"--host"},
			wantErr: "missing value for --host",
		},
		{
			name:    "rejects unknown arg",
			args:    []string{"--bogus"},
			wantErr: "unknown flag: --bogus",
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
			args: []string{"--name", "worker-1", "--host", "dev", "--task", "build", "--color", "rosewater", "--focus"},
			want: SpawnArgs{
				HostExplicit: true,
				Meta: mux.PaneMeta{
					Name:  "worker-1",
					Host:  "dev",
					Task:  "build",
					Color: "rosewater",
				},
				Focus: true,
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
			name: "allows spiral spawn",
			args: []string{"--spiral", "--name", "worker-1", "--host", "dev", "--task", "build", "--color", "rosewater"},
			want: SpawnArgs{
				Spiral:       true,
				HostExplicit: true,
				Meta: mux.PaneMeta{
					Name:  "worker-1",
					Host:  "dev",
					Task:  "build",
					Color: "rosewater",
				},
			},
		},
		{
			name:    "rejects missing color value",
			args:    []string{"--name", "worker-1", "--color"},
			wantErr: "missing value for --color",
		},
		{
			name:    "rejects legacy background flag",
			args:    []string{"--name", "worker-1", "--background"},
			wantErr: "unknown flag: --background",
		},
		{
			name:    "rejects unknown arg",
			args:    []string{"--name", "worker-1", "--bogus"},
			wantErr: "unknown flag: --bogus",
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
