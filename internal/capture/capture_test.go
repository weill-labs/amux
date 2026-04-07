package capture

import (
	"encoding/json"
	"image/color"
	"reflect"
	"strings"
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
			name: "history rewrap pane",
			args: []string{"--history", "--rewrap", "80", "pane-3"},
			want: Request{HistoryMode: true, RewrapSpecified: true, RewrapRaw: "80", RewrapWidth: 80, PaneRef: "pane-3"},
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
			name:    "rewrap requires history",
			req:     Request{RewrapSpecified: true, RewrapRaw: "80", RewrapWidth: 80, PaneRef: "pane-1"},
			wantErr: "--rewrap requires --history",
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
			name: "valid with rewrap",
			req:  Request{HistoryMode: true, RewrapSpecified: true, RewrapRaw: "80", RewrapWidth: 80, PaneRef: "pane-1"},
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
		{
			name:    "rewrap width missing",
			req:     Request{HistoryMode: true, RewrapSpecified: true, PaneRef: "pane-1"},
			wantErr: "--rewrap requires a positive integer width",
		},
		{
			name:    "rewrap width invalid",
			req:     Request{HistoryMode: true, RewrapSpecified: true, RewrapRaw: "wide", PaneRef: "pane-1"},
			wantErr: "--rewrap requires a positive integer width",
		},
		{
			name:    "rewrap width zero",
			req:     Request{HistoryMode: true, RewrapSpecified: true, RewrapRaw: "0", RewrapWidth: 0, PaneRef: "pane-1"},
			wantErr: "--rewrap requires a positive integer width",
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
		ID:            7,
		Name:          "pane-7",
		Active:        true,
		Zoomed:        true,
		Host:          "local",
		Task:          "task",
		Color:         "f5e0dc",
		ColumnIndex:   3,
		ConnStatus:    "connected",
		GitBranch:     "feat/meta",
		PR:            "99",
		TrackedPRs:    []proto.TrackedPR{{Number: 42, Status: proto.TrackedStatusCompleted}},
		TrackedIssues: []proto.TrackedIssue{{ID: "LAB-450", Status: proto.TrackedStatusActive}},
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
			Exited:         true,
			ExitedSince:    "2026-03-20T12:00:05Z",
			Idle:           true,
			IdleSince:      "2026-03-20T12:00:00Z",
			CurrentCommand: "bash",
			LastOutput:     "2026-03-20T11:59:58Z",
		},
	}

	got := BuildPane(input, status)
	want := proto.CapturePane{
		ID:          7,
		Name:        "pane-7",
		Active:      true,
		Zoomed:      true,
		Host:        "local",
		Task:        "task",
		Color:       "f5e0dc",
		ColumnIndex: 3,
		Meta: proto.CaptureMeta{
			Task:          "task",
			GitBranch:     "feat/meta",
			PR:            "99",
			TrackedPRs:    []proto.TrackedPR{{Number: 42, Status: proto.TrackedStatusCompleted}},
			TrackedIssues: []proto.TrackedIssue{{ID: "LAB-450", Status: proto.TrackedStatusActive}},
		},
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
		Exited:         true,
		ExitedSince:    "2026-03-20T12:00:05Z",
		Idle:           true,
		IdleSince:      "2026-03-20T12:00:00Z",
		CurrentCommand: "bash",
		LastOutput:     "2026-03-20T11:59:58Z",
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
	if trackedPRs, ok := meta["tracked_prs"].([]any); !ok || len(trackedPRs) != 1 {
		t.Fatalf("meta.tracked_prs = %#v, want single tracked PR", meta["tracked_prs"])
	}
	if trackedIssues, ok := meta["tracked_issues"].([]any); !ok || len(trackedIssues) != 1 {
		t.Fatalf("meta.tracked_issues = %#v, want single tracked issue", meta["tracked_issues"])
	}

	input.Content[0] = "mutated"
	input.History[0] = "mutated"
	input.TrackedPRs[0].Number = 73
	input.TrackedIssues[0].ID = "LAB-451"
	if got.Content[0] != "screen-1" || got.History[0] != "history-1" {
		t.Fatalf("BuildPane should copy slices, got content=%v history=%v", got.Content, got.History)
	}
	if got.Meta.TrackedPRs[0].Number != 42 || got.Meta.TrackedIssues[0].ID != "LAB-450" {
		t.Fatalf("BuildPane should copy tracked refs, got tracked_prs=%v tracked_issues=%v", got.Meta.TrackedPRs, got.Meta.TrackedIssues)
	}
}

func TestHexColor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   color.Color
		want string
	}{
		{
			name: "nil",
			in:   nil,
			want: "",
		},
		{
			name: "rgba",
			in:   color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff},
			want: "123456",
		},
		{
			name: "rgba64 preserves high byte",
			in:   color.RGBA64{R: 0x1234, G: 0xabcd, B: 0xff01, A: 0xffff},
			want: "12abff",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := hexColor(tt.in); got != tt.want {
				t.Fatalf("hexColor(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func BenchmarkHexColor(b *testing.B) {
	c := color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}

	b.ReportAllocs()
	for b.Loop() {
		_ = hexColor(c)
	}
}

func TestJSONErrorOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		singlePane bool
	}{
		{name: "full screen", singlePane: false},
		{name: "single pane", singlePane: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out := JSONErrorOutput(tt.singlePane, "state_unavailable", "capture state is unavailable")
			var capture struct {
				Error *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(out), &capture); err != nil {
				t.Fatalf("json.Unmarshal(%q): %v", out, err)
			}
			if capture.Error == nil {
				t.Fatalf("expected JSON error payload, got %q", out)
			}
			if capture.Error.Code != "state_unavailable" {
				t.Fatalf("error.code = %q, want state_unavailable", capture.Error.Code)
			}
			if capture.Error.Message != "capture state is unavailable" {
				t.Fatalf("error.message = %q, want capture state is unavailable", capture.Error.Message)
			}
		})
	}
}

func TestValidateJSONOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{name: "blank", raw: " \n\t", wantErr: "capture response was empty"},
		{name: "empty object", raw: "{}", wantErr: "capture response was an empty JSON object"},
		{name: "invalid", raw: "{not-json}", wantErr: "capture response was not valid JSON"},
		{name: "valid object", raw: "{\"session\":\"test\",\"panes\":[]}"},
		{name: "valid error object", raw: JSONErrorOutput(false, "capture_timeout", "timed out")},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateJSONOutput(tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateJSONOutput(%q) error = %v, want nil", tt.raw, err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateJSONOutput(%q) error = %v, want prefix %q", tt.raw, err, tt.wantErr)
			}
		})
	}
}
