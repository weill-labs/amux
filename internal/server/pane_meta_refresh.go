package server

// SetPaneMetaAutoRefresh enables or disables background cwd/git refresh on
// idle transitions for all current sessions.
func (s *Server) SetPaneMetaAutoRefresh(enabled bool) {
	if s == nil {
		return
	}
	for _, sess := range s.sessions {
		sess.DisablePaneMetaAutoRefresh = !enabled
	}
}
