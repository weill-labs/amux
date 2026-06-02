package layout

import (
	"reflect"
	"testing"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/remoteref"
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
			args: []string{"pane-1", "root", "--vertical", "--name", "worker", "--task", "build", "--color", "blue"},
			want: createPaneArgs{
				PaneRef:   "pane-1",
				RootLevel: true,
				Dir:       mux.SplitVertical,
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
			name: "spawn parses auto mode",
			mode: createPaneModeSpawn,
			args: []string{"--auto", "--name", "worker"},
			want: createPaneArgs{
				Auto: true,
				Dir:  mux.SplitVertical,
				Name: "worker",
			},
		},
		{
			name: "spawn parses window target",
			mode: createPaneModeSpawn,
			args: []string{"--window", "logs", "--name", "worker"},
			want: createPaneArgs{
				WindowRef: "logs",
				Dir:       mux.SplitHorizontal,
				Name:      "worker",
			},
		},
		{
			name:    "spawn rejects split-only pane refs",
			mode:    createPaneModeSpawn,
			args:    []string{"pane-1"},
			wantErr: `unknown spawn arg "pane-1"`,
		},
		{
			name:    "spawn rejects --auto with --attach",
			mode:    createPaneModeSpawn,
			args:    []string{"--auto", "--attach", "amux://hetzner-1/main/pane/name/pane-1786"},
			wantErr: "spawn --auto cannot be combined with --attach",
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

func TestParseSpawnAttachArgs(t *testing.T) {
	t.Parallel()

	got, err := ParseSpawnArgs([]string{"--at", "pane-70", "--horizontal", "--attach", "amux://hetzner-1/main/pane/name/pane-1786"})
	if err != nil {
		t.Fatalf("ParseSpawnArgs: %v", err)
	}

	attach := reflect.ValueOf(got).FieldByName("Attach")
	if !attach.IsValid() {
		t.Fatal("SpawnArgs missing Attach field")
	}
	if attach.IsNil() {
		t.Fatal("SpawnArgs.Attach is nil")
	}

	ref := attach.Elem()
	if gotRemote := ref.FieldByName("Remote").String(); gotRemote != "hetzner-1" {
		t.Fatalf("Attach.Remote = %q, want hetzner-1", gotRemote)
	}
	if gotSession := ref.FieldByName("Session").String(); gotSession != "main" {
		t.Fatalf("Attach.Session = %q, want main", gotSession)
	}
	if gotKind := remoteref.Kind(ref.FieldByName("Kind").String()); gotKind != remoteref.KindPane {
		t.Fatalf("Attach.Kind = %q, want pane", gotKind)
	}
	if gotSelectorKind := remoteref.SelectorKind(ref.FieldByName("SelectorKind").String()); gotSelectorKind != remoteref.SelectorName {
		t.Fatalf("Attach.SelectorKind = %q, want name", gotSelectorKind)
	}
	if gotSelector := ref.FieldByName("Selector").String(); gotSelector != "pane-1786" {
		t.Fatalf("Attach.Selector = %q, want pane-1786", gotSelector)
	}
	if got.Meta.Host != "hetzner-1" {
		t.Fatalf("Meta.Host = %q, want hetzner-1", got.Meta.Host)
	}
}

func TestParseRemoteAttachRefRejectsInvalidRefs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
	}{
		{name: "legacy colon ref", value: "hetzner-1:pane-1786"},
		{name: "wrong kind", value: "amux://hetzner-1/main/window/name/main"},
		{name: "missing session", value: "amux://hetzner-1//pane/name/pane-1786"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := ParseRemoteAttachRef(tt.value)
			if err == nil || err.Error() != "spawn --attach requires amux://REMOTE/SESSION/pane/(name|id)/SELECTOR" {
				t.Fatalf("ParseRemoteAttachRef(%q) error = %v, want attach usage", tt.value, err)
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
			args: []string{"pane-1", "root", "--vertical", "--name", "worker", "--task", "build", "--color", "blue", "--focus"},
			want: SplitArgs{
				PaneRef:   "pane-1",
				RootLevel: true,
				Dir:       mux.SplitVertical,
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
			args: []string{"--name", "worker-1", "--task", "build", "--color", "rosewater", "--focus"},
			want: SpawnArgs{
				Dir: mux.SplitVertical,
				Meta: mux.PaneMeta{
					Name:  "worker-1",
					Host:  mux.DefaultHost,
					Task:  "build",
					Color: "rosewater",
				},
				Focus: true,
			},
		},
		{
			name: "parses auto mode",
			args: []string{"--auto", "--name", "worker-1"},
			want: SpawnArgs{
				Auto: true,
				Dir:  mux.SplitVertical,
				Meta: mux.PaneMeta{
					Name: "worker-1",
					Host: mux.DefaultHost,
				},
			},
		},
		{
			name: "parses auto mode with window target",
			args: []string{"--auto", "--window", "logs", "--name", "worker-1"},
			want: SpawnArgs{
				Auto:      true,
				WindowRef: "logs",
				Dir:       mux.SplitVertical,
				Meta: mux.PaneMeta{
					Name: "worker-1",
					Host: mux.DefaultHost,
				},
			},
		},
		{
			name: "parses window targeted spawn placement",
			args: []string{"--window", "logs", "--name", "worker-1"},
			want: SpawnArgs{
				WindowRef: "logs",
				Dir:       mux.SplitHorizontal,
				Meta: mux.PaneMeta{
					Name: "worker-1",
					Host: mux.DefaultHost,
				},
			},
		},
		{
			name: "parses targeted spawn placement",
			args: []string{"--at", "pane-1", "--name", "worker-1"},
			want: SpawnArgs{
				PaneRef: "pane-1",
				Dir:     mux.SplitHorizontal,
				Meta: mux.PaneMeta{
					Name: "worker-1",
					Host: mux.DefaultHost,
				},
			},
		},
		{
			name: "parses root-level targeted spawn placement",
			args: []string{"--at", "pane-1", "--root", "--vertical", "--name", "worker-1"},
			want: SpawnArgs{
				PaneRef:   "pane-1",
				RootLevel: true,
				Dir:       mux.SplitVertical,
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
				Dir: mux.SplitVertical,
				Meta: mux.PaneMeta{
					Host: mux.DefaultHost,
					Task: "build",
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
		{
			name: "parses auto mode with pane target as window hint",
			args: []string{"--auto", "--at", "pane-1", "--name", "worker-1"},
			want: SpawnArgs{
				Auto:    true,
				PaneRef: "pane-1",
				Dir:     mux.SplitVertical,
				Meta: mux.PaneMeta{
					Name: "worker-1",
					Host: mux.DefaultHost,
				},
			},
		},
		{
			name:    "rejects window with explicit pane target",
			args:    []string{"--window", "logs", "--at", "pane-1"},
			wantErr: "spawn --window cannot be combined with --at",
		},
		{
			name:    "rejects spiral flag",
			args:    []string{"--spiral"},
			wantErr: "unknown flag: --spiral",
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
