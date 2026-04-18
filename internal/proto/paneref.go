package proto

import (
	"fmt"
	"strings"
)

type PaneRef struct {
	Host string
	Pane string
}

func ParsePaneRef(s string) (PaneRef, error) {
	if s == "" {
		return PaneRef{}, nil
	}
	if !strings.Contains(s, "/") {
		return PaneRef{Pane: s}, nil
	}
	host, pane, _ := strings.Cut(s, "/")
	switch {
	case host == "":
		return PaneRef{}, fmt.Errorf("invalid pane ref %q: missing host", s)
	case pane == "":
		return PaneRef{}, fmt.Errorf("invalid pane ref %q: missing pane", s)
	default:
		return PaneRef{Host: host, Pane: pane}, nil
	}
}
