package mux

import (
	"encoding/json"
	"fmt"

	"github.com/weill-labs/amux/internal/proto"
)

const (
	PaneMetaKeyTask          = "task"
	PaneMetaKeyBranch        = "branch"
	PaneMetaKeyPR            = "pr"
	PaneMetaKeyTrackedPRs    = "tracked_prs"
	PaneMetaKeyTrackedIssues = "tracked_issues"
)

func clonePaneMetaKV(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func formatTrackedPRsValue(prs []proto.TrackedPR) string {
	if len(prs) == 0 {
		return ""
	}
	data, _ := json.Marshal(prs)
	return string(data)
}

func formatTrackedIssuesValue(issues []proto.TrackedIssue) string {
	if len(issues) == 0 {
		return ""
	}
	data, _ := json.Marshal(issues)
	return string(data)
}

func parseTrackedPRsValue(raw string) ([]proto.TrackedPR, error) {
	if raw == "" {
		return nil, nil
	}
	var prs []proto.TrackedPR
	if err := json.Unmarshal([]byte(raw), &prs); err != nil {
		return nil, err
	}
	return prs, nil
}

func parseTrackedIssuesValue(raw string) ([]proto.TrackedIssue, error) {
	if raw == "" {
		return nil, nil
	}
	var issues []proto.TrackedIssue
	if err := json.Unmarshal([]byte(raw), &issues); err != nil {
		return nil, err
	}
	return issues, nil
}

func clonePaneMeta(meta *PaneMeta) PaneMeta {
	if meta == nil {
		return PaneMeta{}
	}
	next := *meta
	next.KV = clonePaneMetaKV(meta.KV)
	next.TrackedPRs = proto.CloneTrackedPRs(meta.TrackedPRs)
	next.TrackedIssues = proto.CloneTrackedIssues(meta.TrackedIssues)
	return next
}

func hydrateReservedKV(meta *PaneMeta) {
	if meta == nil {
		return
	}
	if meta.KV == nil {
		meta.KV = map[string]string{}
	}
	if meta.Task != "" {
		meta.KV[PaneMetaKeyTask] = meta.Task
	}
	if meta.GitBranch != "" {
		meta.KV[PaneMetaKeyBranch] = meta.GitBranch
	}
	if meta.PR != "" {
		meta.KV[PaneMetaKeyPR] = meta.PR
	}
	if len(meta.TrackedPRs) > 0 {
		meta.KV[PaneMetaKeyTrackedPRs] = formatTrackedPRsValue(meta.TrackedPRs)
	}
	if len(meta.TrackedIssues) > 0 {
		meta.KV[PaneMetaKeyTrackedIssues] = formatTrackedIssuesValue(meta.TrackedIssues)
	}
}

func applyPaneMetaKV(meta *PaneMeta) error {
	if meta == nil {
		return nil
	}

	if raw, ok := meta.KV[PaneMetaKeyTask]; ok {
		meta.Task = raw
	} else {
		meta.Task = ""
	}

	if raw, ok := meta.KV[PaneMetaKeyBranch]; ok {
		meta.GitBranch = raw
	} else {
		meta.GitBranch = ""
	}

	if raw, ok := meta.KV[PaneMetaKeyPR]; ok {
		meta.PR = raw
	} else {
		meta.PR = ""
	}

	if raw, ok := meta.KV[PaneMetaKeyTrackedPRs]; ok {
		prs, err := parseTrackedPRsValue(raw)
		if err != nil {
			return fmt.Errorf("invalid %s value: %w", PaneMetaKeyTrackedPRs, err)
		}
		meta.TrackedPRs = prs
		if len(prs) == 0 {
			delete(meta.KV, PaneMetaKeyTrackedPRs)
		} else {
			meta.KV[PaneMetaKeyTrackedPRs] = formatTrackedPRsValue(prs)
		}
	} else {
		meta.TrackedPRs = nil
	}

	if raw, ok := meta.KV[PaneMetaKeyTrackedIssues]; ok {
		issues, err := parseTrackedIssuesValue(raw)
		if err != nil {
			return fmt.Errorf("invalid %s value: %w", PaneMetaKeyTrackedIssues, err)
		}
		meta.TrackedIssues = issues
		if len(issues) == 0 {
			delete(meta.KV, PaneMetaKeyTrackedIssues)
		} else {
			meta.KV[PaneMetaKeyTrackedIssues] = formatTrackedIssuesValue(issues)
		}
	} else {
		meta.TrackedIssues = nil
	}

	if len(meta.KV) == 0 {
		meta.KV = nil
	}
	return nil
}

func NormalizePaneMeta(meta *PaneMeta) (manualBranch bool, err error) {
	if meta != nil && meta.KV != nil {
		_, manualBranch = meta.KV[PaneMetaKeyBranch]
	}
	hydrateReservedKV(meta)
	if err := applyPaneMetaKV(meta); err != nil {
		return false, err
	}
	return manualBranch, nil
}

func SetPaneMetaKV(meta *PaneMeta, key, value string) (manualBranch bool, err error) {
	next := clonePaneMeta(meta)
	hydrateReservedKV(&next)
	next.KV[key] = value
	if err := applyPaneMetaKV(&next); err != nil {
		return false, err
	}
	_, manualBranch = next.KV[PaneMetaKeyBranch]
	*meta = next
	return manualBranch, nil
}

func RemovePaneMetaKV(meta *PaneMeta, key string) (manualBranch bool, err error) {
	next := clonePaneMeta(meta)
	hydrateReservedKV(&next)
	delete(next.KV, key)
	if len(next.KV) == 0 {
		next.KV = nil
	}
	if err := applyPaneMetaKV(&next); err != nil {
		return false, err
	}
	_, manualBranch = next.KV[PaneMetaKeyBranch]
	*meta = next
	return manualBranch, nil
}
func FormatTrackedPRsValue(prs []proto.TrackedPR) string {
	return formatTrackedPRsValue(prs)
}

func FormatTrackedIssuesValue(issues []proto.TrackedIssue) string {
	return formatTrackedIssuesValue(issues)
}
