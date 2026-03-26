package proto

// TrackedStatus is the cached external completion state for a tracked ref.
type TrackedStatus string

const (
	TrackedStatusActive    TrackedStatus = "active"
	TrackedStatusCompleted TrackedStatus = "completed"
	TrackedStatusUnknown   TrackedStatus = "unknown"
)

// TrackedPR is one PR reference tracked on a pane.
type TrackedPR struct {
	Number    int           `json:"number"`
	Status    TrackedStatus `json:"status,omitempty"`
	Stale     bool          `json:"stale,omitempty"`
	CheckedAt string        `json:"checked_at,omitempty"`
}

// TrackedIssue is one issue reference tracked on a pane.
type TrackedIssue struct {
	ID        string        `json:"id"`
	Status    TrackedStatus `json:"status,omitempty"`
	Stale     bool          `json:"stale,omitempty"`
	CheckedAt string        `json:"checked_at,omitempty"`
}

func CloneTrackedPRs(src []TrackedPR) []TrackedPR {
	return append([]TrackedPR(nil), src...)
}

func CloneTrackedIssues(src []TrackedIssue) []TrackedIssue {
	return append([]TrackedIssue(nil), src...)
}

func TrackedPRNumbers(src []TrackedPR) []int {
	out := make([]int, 0, len(src))
	for _, ref := range src {
		out = append(out, ref.Number)
	}
	return out
}

func TrackedIssueIDs(src []TrackedIssue) []string {
	out := make([]string, 0, len(src))
	for _, ref := range src {
		out = append(out, ref.ID)
	}
	return out
}
