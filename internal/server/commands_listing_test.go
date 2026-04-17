package server

import (
	"fmt"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
	listingcmd "github.com/weill-labs/amux/internal/server/commands/listing"
)

func TestToListingPaneEntryReusesMetadataViews(t *testing.T) {
	t.Parallel()

	entry := paneListEntry{
		paneID: 1,
		name:   "pane-1",
		kv: map[string]string{
			"issue": "LAB-1349",
		},
		prs: []proto.TrackedPR{
			{Number: 42},
		},
		issues: []proto.TrackedIssue{
			{ID: "LAB-1349"},
		},
	}

	got := toListingPaneEntry(entry)

	entry.kv["owner"] = "codex"
	entry.prs[0].Number = 73
	entry.issues[0].ID = "LAB-1350"

	if got.KV["owner"] != "codex" {
		t.Fatalf("KV[owner] = %q, want codex", got.KV["owner"])
	}
	if got.TrackedPRs[0].Number != 73 {
		t.Fatalf("TrackedPRs[0].Number = %d, want 73", got.TrackedPRs[0].Number)
	}
	if got.TrackedIssues[0].ID != "LAB-1350" {
		t.Fatalf("TrackedIssues[0].ID = %q, want LAB-1350", got.TrackedIssues[0].ID)
	}
}

var benchmarkListingPaneEntries []listingcmd.PaneEntry

func BenchmarkToListingPaneEntries(b *testing.B) {
	for _, paneCount := range []int{32, 256} {
		b.Run(fmt.Sprintf("%d-panes", paneCount), func(b *testing.B) {
			entries := benchmarkPaneListEntries(paneCount)
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				got := toListingPaneEntries(entries)
				if len(got) != len(entries) {
					b.Fatalf("len(toListingPaneEntries()) = %d, want %d", len(got), len(entries))
				}
				benchmarkListingPaneEntries = got
			}
		})
	}
}

func benchmarkPaneListEntries(count int) []paneListEntry {
	entries := make([]paneListEntry, 0, count)
	for i := 0; i < count; i++ {
		entries = append(entries, paneListEntry{
			paneID:     uint32(i + 1),
			name:       fmt.Sprintf("pane-%d", i+1),
			host:       "local",
			windowName: fmt.Sprintf("window-%d", i%8+1),
			task:       fmt.Sprintf("LAB-%d", 1300+i),
			cwd:        fmt.Sprintf("/tmp/project-%d", i),
			gitBranch:  fmt.Sprintf("feature/%d", i),
			idle:       "2m",
			pr:         fmt.Sprintf("%d", 400+i),
			kv: map[string]string{
				"branch":   fmt.Sprintf("feature/%d", i),
				"issue":    fmt.Sprintf("LAB-%d", 1300+i),
				"owner":    "codex",
				"pr":       fmt.Sprintf("%d", 400+i),
				"priority": "high",
				"task":     fmt.Sprintf("LAB-%d", 1300+i),
			},
			prs: []proto.TrackedPR{
				{Number: 400 + i},
				{Number: 500 + i},
			},
			issues: []proto.TrackedIssue{
				{ID: fmt.Sprintf("LAB-%d", 1300+i)},
				{ID: fmt.Sprintf("LAB-%d", 2300+i)},
			},
			active: i == 0,
			lead:   i%4 == 0,
		})
	}
	return entries
}
