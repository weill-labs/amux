package listing

import (
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
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
			name: "tracked refs only",
			entry: PaneEntry{
				Name: "pane-1",
				TrackedPRs: []proto.TrackedPR{
					{Number: 42},
				},
				TrackedIssues: []proto.TrackedIssue{
					{ID: "LAB-499"},
				},
			},
			want: "prs=[42] issues=[LAB-499]",
		},
		{
			name: "lead with tracked refs",
			entry: PaneEntry{
				Name: "pane-1",
				Lead: true,
				TrackedPRs: []proto.TrackedPR{
					{Number: 42},
				},
				TrackedIssues: []proto.TrackedIssue{
					{ID: "LAB-499"},
				},
			},
			want: "lead prs=[42] issues=[LAB-499]",
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
