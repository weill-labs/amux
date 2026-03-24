package server

import (
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

const defaultPaneLogSize = 100

const (
	paneLogEventCreate = "create"
	paneLogEventExit   = "exit"
)

// PaneLogEntry records a pane lifecycle event.
type PaneLogEntry struct {
	Timestamp  time.Time
	Event      string
	PaneID     uint32
	PaneName   string
	Host       string
	Cwd        string
	GitBranch  string
	ExitReason string
}

// PaneLog is a circular buffer of pane lifecycle events.
type PaneLog struct {
	entries []PaneLogEntry
	start   int
	length  int
}

func newPaneLog(capacity int) *PaneLog {
	if capacity <= 0 {
		capacity = defaultPaneLogSize
	}
	return &PaneLog{entries: make([]PaneLogEntry, capacity)}
}

func (l *PaneLog) Append(entry PaneLogEntry) {
	if l == nil {
		return
	}
	if len(l.entries) == 0 {
		*l = *newPaneLog(defaultPaneLogSize)
	}
	if len(l.entries) == 0 {
		return
	}

	idx := (l.start + l.length) % len(l.entries)
	if l.length == len(l.entries) {
		idx = l.start
		l.start = (l.start + 1) % len(l.entries)
	} else {
		l.length++
	}
	l.entries[idx] = entry
}

func (l *PaneLog) Snapshot() []PaneLogEntry {
	if l == nil || l.length == 0 {
		return nil
	}
	out := make([]PaneLogEntry, 0, l.length)
	for i := 0; i < l.length; i++ {
		idx := (l.start + i) % len(l.entries)
		out = append(out, l.entries[idx])
	}
	return out
}

func (s *Session) ensurePaneLog() *PaneLog {
	if s.paneLog == nil {
		s.paneLog = newPaneLog(defaultPaneLogSize)
	}
	return s.paneLog
}

func effectivePaneCwd(pane *mux.Pane) string {
	if pane == nil {
		return ""
	}
	if cwd := pane.LiveCwd(); cwd != "" {
		return cwd
	}
	return pane.Meta.Dir
}

func (s *Session) appendPaneLog(event string, pane *mux.Pane, reason string) {
	entry := PaneLogEntry{
		Timestamp:  time.Now().UTC(),
		Event:      event,
		PaneID:     pane.ID,
		PaneName:   pane.Meta.Name,
		Host:       pane.Meta.Host,
		ExitReason: reason,
	}
	if event == paneLogEventExit {
		entry.Cwd = effectivePaneCwd(pane)
		entry.GitBranch = pane.Meta.GitBranch
	}
	s.ensurePaneLog().Append(entry)
}
