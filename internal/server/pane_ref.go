package server

import (
	"fmt"

	"github.com/weill-labs/amux/internal/proto"
)

func (s *Session) queryPaneRef(ref string) (proto.PaneRef, error) {
	return enqueueSessionQuery(s, func(s *Session) (proto.PaneRef, error) {
		return s.parsePaneRef(ref)
	})
}

func (s *Session) parsePaneRef(ref string) (proto.PaneRef, error) {
	parsed, err := proto.ParsePaneRef(ref)
	if err != nil {
		return proto.PaneRef{}, err
	}
	if parsed.Host != "" || parsed.Pane == "" {
		return parsed, nil
	}

	if !s.isKnownRemoteHost(parsed.Pane) {
		return parsed, nil
	}
	if s.hasExactPaneName(parsed.Pane) {
		return proto.PaneRef{}, fmt.Errorf("ambiguous pane ref %q: matches both a remote host and a local pane; use host/pane or rename the local pane", ref)
	}
	return proto.PaneRef{Host: parsed.Pane}, nil
}

func (s *Session) isKnownRemoteHost(hostName string) bool {
	if s == nil || s.RemoteManager == nil || hostName == "" {
		return false
	}
	_, ok := s.RemoteManager.AllHostStatus()[hostName]
	return ok
}

func (s *Session) hasExactPaneName(name string) bool {
	if s == nil || name == "" {
		return false
	}
	for _, pane := range s.Panes {
		if pane != nil && pane.Meta.Name == name {
			return true
		}
	}
	return false
}
