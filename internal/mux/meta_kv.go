package mux

import "sort"

// ReservedMetaKeys are modeled directly on PaneMeta fields and exposed through
// the generic meta command surface.
var ReservedMetaKeys = map[string]struct{}{
	"task":   {},
	"branch": {},
	"pr":     {},
}

func CloneMetaKV(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

// NormalizedMetaKV returns the user-facing metadata map for a pane, combining
// generic keys with the reserved task/branch/pr fields.
func NormalizedMetaKV(meta PaneMeta) map[string]string {
	kv := CloneMetaKV(meta.KV)
	if meta.Task != "" {
		if kv == nil {
			kv = make(map[string]string, 1)
		}
		kv["task"] = meta.Task
	}
	if meta.GitBranch != "" {
		if kv == nil {
			kv = make(map[string]string, 1)
		}
		kv["branch"] = meta.GitBranch
	}
	if meta.PR != "" {
		if kv == nil {
			kv = make(map[string]string, 1)
		}
		kv["pr"] = meta.PR
	}
	return kv
}

func SortedMetaKeys(meta PaneMeta) []string {
	kv := NormalizedMetaKV(meta)
	if len(kv) == 0 {
		return nil
	}
	keys := make([]string, 0, len(kv))
	for key := range kv {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
