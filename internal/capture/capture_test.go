package capture

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func TestParseArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want Request
	}{
		{
			name: "empty",
			want: Request{},
		},
		{
			name: "pane with ansi",
			args: []string{"--ansi", "pane-1"},
			want: Request{IncludeANSI: true, PaneRef: "pane-1"},
		},
		{
			name: "format json pane",
			args: []string{"--format", "json", "pane-2"},
			want: Request{FormatJSON: true, PaneRef: "pane-2"},
		},
		{
			name: "history pane",
			args: []string{"--history", "pane-3"},
			want: Request{HistoryMode: true, PaneRef: "pane-3"},
		},
		{
			name: "display",
			args: []string{"--display"},
			want: Request{DisplayMode: true},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ParseArgs(tt.args); got != tt.want {
				t.Fatalf("ParseArgs(%v) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}

func TestValidateScreenRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     Request
		wantErr string
	}{
		{
			name: "plain pane",
			req:  Request{PaneRef: "pane-1"},
		},
		{
			name:    "ansi and colors",
			req:     Request{IncludeANSI: true, ColorMap: true},
			wantErr: "--ansi, --colors, and --format json are mutually exclusive",
		},
		{
			name:    "display with pane",
			req:     Request{DisplayMode: true, PaneRef: "pane-1"},
			wantErr: "--display is mutually exclusive with other flags",
		},
		{
			name:    "display with history",
			req:     Request{DisplayMode: true, HistoryMode: true},
			wantErr: "--display is mutually exclusive with other flags",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateScreenRequest(tt.req)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateScreenRequest(%+v) error = %v, want nil", tt.req, err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("ValidateScreenRequest(%+v) error = %v, want %q", tt.req, err, tt.wantErr)
			}
		})
	}
}

func TestValidateHistoryRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     Request
		wantErr string
	}{
		{
			name: "valid",
			req:  Request{HistoryMode: true, PaneRef: "pane-1"},
		},
		{
			name:    "missing history flag",
			req:     Request{PaneRef: "pane-1"},
			wantErr: "internal error: captureHistory called without --history",
		},
		{
			name:    "ansi not allowed",
			req:     Request{HistoryMode: true, IncludeANSI: true, PaneRef: "pane-1"},
			wantErr: "--history is mutually exclusive with --ansi, --colors, and --display",
		},
		{
			name:    "missing pane ref",
			req:     Request{HistoryMode: true},
			wantErr: "--history requires a pane target",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateHistoryRequest(tt.req)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateHistoryRequest(%+v) error = %v, want nil", tt.req, err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("ValidateHistoryRequest(%+v) error = %v, want %q", tt.req, err, tt.wantErr)
			}
		})
	}
}

func TestBuildPane(t *testing.T) {
	t.Parallel()

	input := PaneInput{
		ID:         7,
		Name:       "pane-7",
		Active:     true,
		Minimized:  false,
		Zoomed:     true,
		Host:       "local",
		Task:       "task",
		Color:      "f5e0dc",
		ConnStatus: "connected",
		GitBranch:  "feat/meta",
		PR:         "99",
		Cursor: proto.CaptureCursor{
			Col:    4,
			Row:    2,
			Hidden: true,
		},
		Content: []string{"screen-1", "screen-2"},
		History: []string{"history-1"},
	}
	status := map[uint32]proto.PaneAgentStatus{
		7: {
			Idle:           true,
			IdleSince:      "2026-03-20T12:00:00Z",
			CurrentCommand: "bash",
			ChildPIDs:      nil,
		},
	}

	got := BuildPane(input, status)
	want := proto.CapturePane{
		ID:         7,
		Name:       "pane-7",
		Active:     true,
		Minimized:  false,
		Zoomed:     true,
		Host:       "local",
		Task:       "task",
		Color:      "f5e0dc",
		ConnStatus: "connected",
		GitBranch:  "feat/meta",
		PR:         "99",
		Cursor: proto.CaptureCursor{
			Col:    4,
			Row:    2,
			Hidden: true,
		},
		Content:        []string{"screen-1", "screen-2"},
		History:        []string{"history-1"},
		Idle:           true,
		IdleSince:      "2026-03-20T12:00:00Z",
		CurrentCommand: "bash",
		ChildPIDs:      []int{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildPane() = %+v, want %+v", got, want)
	}

	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	metaValue, ok := payload["meta"]
	if !ok {
		t.Fatalf("BuildPane JSON missing meta field: %#v", payload)
	}
	meta, ok := metaValue.(map[string]any)
	if !ok {
		t.Fatalf("meta = %#v, want map", metaValue)
	}
	if meta["task"] != "task" {
		t.Fatalf("meta.task = %#v, want %q", meta["task"], "task")
	}
	if meta["git_branch"] != "feat/meta" {
		t.Fatalf("meta.git_branch = %#v, want %q", meta["git_branch"], "feat/meta")
	}
	if meta["pr"] != "99" {
		t.Fatalf("meta.pr = %#v, want %q", meta["pr"], "99")
	}

	input.Content[0] = "mutated"
	input.History[0] = "mutated"
	if got.Content[0] != "screen-1" || got.History[0] != "history-1" {
		t.Fatalf("BuildPane should copy slices, got content=%v history=%v", got.Content, got.History)
	}
}
