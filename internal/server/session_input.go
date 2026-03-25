package server

import "github.com/weill-labs/amux/internal/mux"

func (s *Session) activeInputPane() *mux.Pane {
	w := s.activeWindow()
	if w == nil {
		return nil
	}
	return w.ActivePane
}

func (s *Session) refreshInputTarget() {
	s.ensureInputRouter().refreshTarget(s.activeInputPane())
}

func (s *Session) inputTargetPane() *mux.Pane {
	return s.ensureInputRouter().targetPane()
}
