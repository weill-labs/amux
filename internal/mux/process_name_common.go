package mux

import "strings"

func normalizeProcessName(name string) string {
	name = strings.TrimSpace(name)
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	return strings.TrimPrefix(name, "-")
}
