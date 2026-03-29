package meta

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/weill-labs/amux/internal/proto"
)

type CollectionUpdate struct {
	PRs    []int
	Issues []string
}

func ParseCollectionArgs(kvPairs []string) (CollectionUpdate, error) {
	var update CollectionUpdate
	for _, kv := range kvPairs {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			return CollectionUpdate{}, fmt.Errorf("invalid key=value: %q", kv)
		}
		switch key {
		case "pr":
			pr, err := ParsePR(value)
			if err != nil {
				return CollectionUpdate{}, err
			}
			update.PRs = append(update.PRs, pr)
		case "issue":
			issue, err := ParseIssue(value)
			if err != nil {
				return CollectionUpdate{}, err
			}
			update.Issues = append(update.Issues, issue)
		default:
			return CollectionUpdate{}, fmt.Errorf("unknown meta key: %q (valid: pr, issue)", key)
		}
	}
	return update, nil
}

func ParsePR(value string) (int, error) {
	pr, err := strconv.Atoi(value)
	if err != nil || pr <= 0 {
		return 0, fmt.Errorf("invalid pr value: %q", value)
	}
	return pr, nil
}

func ParseIssue(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("invalid issue value: %q", value)
	}
	return value, nil
}

func UpsertTrackedPR(values []proto.TrackedPR, target int) []proto.TrackedPR {
	for i := range values {
		if values[i].Number == target {
			return values
		}
	}
	return append(values, proto.TrackedPR{Number: target, Status: proto.TrackedStatusUnknown})
}

func UpsertTrackedIssue(values []proto.TrackedIssue, target string) []proto.TrackedIssue {
	for i := range values {
		if values[i].ID == target {
			return values
		}
	}
	return append(values, proto.TrackedIssue{ID: target, Status: proto.TrackedStatusUnknown})
}

func RemoveTrackedPR(values []proto.TrackedPR, target int) []proto.TrackedPR {
	return slices.DeleteFunc(values, func(value proto.TrackedPR) bool {
		return value.Number == target
	})
}

func RemoveTrackedIssue(values []proto.TrackedIssue, target string) []proto.TrackedIssue {
	return slices.DeleteFunc(values, func(value proto.TrackedIssue) bool {
		return value.ID == target
	})
}

func FormatCollections(prs []proto.TrackedPR, issues []proto.TrackedIssue) string {
	var parts []string
	if len(prs) > 0 {
		values := make([]string, 0, len(prs))
		for _, pr := range prs {
			values = append(values, strconv.Itoa(pr.Number))
		}
		parts = append(parts, "prs=["+strings.Join(values, ",")+"]")
	}
	if len(issues) > 0 {
		values := make([]string, 0, len(issues))
		for _, issue := range issues {
			values = append(values, issue.ID)
		}
		parts = append(parts, "issues=["+strings.Join(values, ",")+"]")
	}
	return strings.Join(parts, " ")
}
