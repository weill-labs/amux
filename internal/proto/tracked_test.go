package proto

import (
	"reflect"
	"testing"
)

func TestTrackedRefHelpers(t *testing.T) {
	t.Parallel()

	prs := []TrackedPR{
		{Number: 42, Status: TrackedStatusCompleted, Stale: true, CheckedAt: "pr-check"},
		{Number: 314, Status: TrackedStatusActive},
	}
	issues := []TrackedIssue{
		{ID: "LAB-450", Status: TrackedStatusCompleted, CheckedAt: "issue-check"},
		{ID: "LAB-451", Status: TrackedStatusUnknown, Stale: true},
	}

	clonedPRs := CloneTrackedPRs(prs)
	clonedIssues := CloneTrackedIssues(issues)

	if !reflect.DeepEqual(clonedPRs, prs) {
		t.Fatalf("CloneTrackedPRs() = %#v, want %#v", clonedPRs, prs)
	}
	if !reflect.DeepEqual(clonedIssues, issues) {
		t.Fatalf("CloneTrackedIssues() = %#v, want %#v", clonedIssues, issues)
	}

	clonedPRs[0].Number = 7
	clonedIssues[0].ID = "LAB-999"
	if prs[0].Number != 42 {
		t.Fatalf("CloneTrackedPRs should return a copy, source mutated to %#v", prs)
	}
	if issues[0].ID != "LAB-450" {
		t.Fatalf("CloneTrackedIssues should return a copy, source mutated to %#v", issues)
	}

	if got := TrackedPRNumbers(prs); !reflect.DeepEqual(got, []int{42, 314}) {
		t.Fatalf("TrackedPRNumbers() = %v, want [42 314]", got)
	}
	if got := TrackedIssueIDs(issues); !reflect.DeepEqual(got, []string{"LAB-450", "LAB-451"}) {
		t.Fatalf("TrackedIssueIDs() = %v, want [LAB-450 LAB-451]", got)
	}
}
