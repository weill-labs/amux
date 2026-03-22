package meta

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
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
			if value == "" {
				return CollectionUpdate{}, fmt.Errorf("invalid issue value: %q", value)
			}
			update.Issues = append(update.Issues, value)
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

func RemoveIntValue(values []int, target int) []int {
	return slices.DeleteFunc(values, func(value int) bool {
		return value == target
	})
}

func RemoveStringValue(values []string, target string) []string {
	return slices.DeleteFunc(values, func(value string) bool {
		return value == target
	})
}

func FormatCollections(prs []int, issues []string) string {
	var parts []string
	if len(prs) > 0 {
		values := make([]string, 0, len(prs))
		for _, pr := range prs {
			values = append(values, strconv.Itoa(pr))
		}
		parts = append(parts, "prs=["+strings.Join(values, ",")+"]")
	}
	if len(issues) > 0 {
		parts = append(parts, "issues=["+strings.Join(issues, ",")+"]")
	}
	return strings.Join(parts, " ")
}
