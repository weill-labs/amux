package listing

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFormatPaneListMeta(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		entry PaneEntry
		want  string
	}{
		{
			name: "no metadata",
			entry: PaneEntry{
				Name: "pane-1",
			},
			want: "",
		},
		{
			name: "lead only",
			entry: PaneEntry{
				Name: "pane-1",
				Lead: true,
			},
			want: "lead",
		},
		{
			name: "generic kv only",
			entry: PaneEntry{
				Name: "pane-1",
				KV:   map[string]string{"issue": "LAB-499"},
			},
			want: "issue=LAB-499",
		},
		{
			name: "lead with generic kv",
			entry: PaneEntry{
				Name: "pane-1",
				Lead: true,
				KV:   map[string]string{"issue": "LAB-499"},
			},
			want: "lead issue=LAB-499",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := formatPaneListMeta(tt.entry); got != tt.want {
				t.Fatalf("formatPaneListMeta() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatPaneListPreservesPaneNameColumnWhenLeadPresent(t *testing.T) {
	t.Parallel()

	out := FormatPaneList([]PaneEntry{{
		PaneID:     2,
		Name:       "pane-2",
		Host:       "local",
		WindowName: "window-2",
		Lead:       true,
	}}, "", false)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("FormatPaneList() returned %d lines, want 2:\n%s", len(lines), out)
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 2 {
		t.Fatalf("row fields = %v, want at least pane id and name", fields)
	}
	if got := fields[1]; got != "pane-2" {
		t.Fatalf("pane name field = %q, want %q", got, "pane-2")
	}
	if !strings.Contains(lines[1], "lead") {
		t.Fatalf("row should include lead metadata, got: %q", lines[1])
	}
}

func TestFormatWindowNameLeavesZoomMarkerOutOfName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		window string
		zoomed bool
		want   string
	}{
		{
			name:   "normal window",
			window: "main",
			zoomed: false,
			want:   "main",
		},
		{
			name:   "zoomed window",
			window: "main",
			zoomed: true,
			want:   "main",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := FormatWindowName(tt.window, tt.zoomed); got != tt.want {
				t.Fatalf("FormatWindowName(%q, %v) = %q, want %q", tt.window, tt.zoomed, got, tt.want)
			}
		})
	}
}

func TestFormatPaneListMarksZoomedWindow(t *testing.T) {
	t.Parallel()

	out := FormatPaneList([]PaneEntry{
		{
			PaneID:       1,
			Name:         "pane-1",
			Host:         "local",
			WindowName:   "main",
			WindowZoomed: true,
		},
		{
			PaneID:     2,
			Name:       "pane-2",
			Host:       "local",
			WindowName: "logs",
		},
	}, "", false)

	if strings.Contains(out, "mainZ") {
		t.Fatalf("zoomed window name should not include Z marker:\n%s", out)
	}
	if strings.Contains(out, "logsZ") {
		t.Fatalf("unzoomed window should not include Z marker:\n%s", out)
	}
	if !strings.Contains(out, "WINDOW") || !strings.Contains(out, "ZOOM") {
		t.Fatalf("list should include separate WINDOW and ZOOM headers:\n%s", out)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("FormatPaneList() returned %d lines, want 3:\n%s", len(lines), out)
	}
	if got := fixedListColumn(t, lines[0], lines[1], "WINDOW", "ZOOM"); got != "main" {
		t.Fatalf("zoomed row WINDOW column = %q, want main\n%s", got, out)
	}
	if got := fixedListColumn(t, lines[0], lines[1], "ZOOM", "TASK"); got != "Z" {
		t.Fatalf("zoomed row ZOOM column = %q, want Z\n%s", got, out)
	}
	if got := fixedListColumn(t, lines[0], lines[2], "WINDOW", "ZOOM"); got != "logs" {
		t.Fatalf("unzoomed row WINDOW column = %q, want logs\n%s", got, out)
	}
	if got := fixedListColumn(t, lines[0], lines[2], "ZOOM", "TASK"); got != "" {
		t.Fatalf("unzoomed row ZOOM column = %q, want empty\n%s", got, out)
	}
}

func TestListJSONIncludesCleanWindowAndZoomedFlag(t *testing.T) {
	t.Parallel()

	res := List(fakeListContext{entries: []PaneEntry{{
		PaneID:       1,
		Name:         "pane-1",
		Host:         "local",
		WindowName:   "main",
		WindowZoomed: true,
		Task:         "build",
		Active:       true,
	}}}, []string{"--json"})
	if res.Err != nil {
		t.Fatalf("List(--json) error = %v", res.Err)
	}

	var rows []map[string]any
	if err := json.Unmarshal([]byte(res.Output), &rows); err != nil {
		t.Fatalf("json.Unmarshal(List --json) = %v\n%s", err, res.Output)
	}
	if len(rows) != 1 {
		t.Fatalf("List --json returned %d rows, want 1\n%s", len(rows), res.Output)
	}
	if got := rows[0]["window"]; got != "main" {
		t.Fatalf("json window = %#v, want main\n%s", got, res.Output)
	}
	if got := rows[0]["window_zoomed"]; got != true {
		t.Fatalf("json window_zoomed = %#v, want true\n%s", got, res.Output)
	}
}

func fixedListColumn(t *testing.T, header, row, column, nextColumn string) string {
	t.Helper()

	start := strings.Index(header, column)
	if start < 0 {
		t.Fatalf("header missing %q: %q", column, header)
	}
	end := len(row)
	if nextColumn != "" {
		next := strings.Index(header, nextColumn)
		if next < 0 {
			t.Fatalf("header missing next column %q: %q", nextColumn, header)
		}
		if next > start {
			end = min(next, len(row))
		}
	}
	if start >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[start:end])
}

type fakeListContext struct {
	entries []PaneEntry
}

func (f fakeListContext) HomeDir() string {
	return ""
}

func (f fakeListContext) BuildVersion() string {
	return ""
}

func (f fakeListContext) QueryPaneList() ([]PaneEntry, error) {
	return f.entries, nil
}

func (f fakeListContext) QuerySessionStatus() (SessionStatus, error) {
	return SessionStatus{}, nil
}

func (f fakeListContext) QueryWindowList() ([]WindowEntry, error) {
	return nil, nil
}

func (f fakeListContext) QueryClientList() ([]ClientEntry, error) {
	return nil, nil
}

func (f fakeListContext) QueryConnectionLog() ([]ConnectionLogEntry, error) {
	return nil, nil
}

func (f fakeListContext) QueryPaneLog() ([]PaneLogEntry, error) {
	return nil, nil
}
