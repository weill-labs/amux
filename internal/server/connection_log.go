package server

import "time"

const defaultConnectionLogSize = 100

const (
	connectionLogEventAttach = "attach"
	connectionLogEventDetach = "detach"
)

type ConnectionLogEntry struct {
	Timestamp        time.Time
	Event            string
	ClientID         string
	Cols             int
	Rows             int
	DisconnectReason string
}

type ConnectionLog struct {
	entries []ConnectionLogEntry
	start   int
	length  int
}

func newConnectionLog(capacity int) *ConnectionLog {
	if capacity <= 0 {
		capacity = defaultConnectionLogSize
	}
	return &ConnectionLog{entries: make([]ConnectionLogEntry, capacity)}
}

func (l *ConnectionLog) Append(entry ConnectionLogEntry) {
	if l == nil {
		return
	}
	if len(l.entries) == 0 {
		*l = *newConnectionLog(defaultConnectionLogSize)
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

func (l *ConnectionLog) Snapshot() []ConnectionLogEntry {
	if l == nil || l.length == 0 {
		return nil
	}
	out := make([]ConnectionLogEntry, 0, l.length)
	for i := 0; i < l.length; i++ {
		idx := (l.start + i) % len(l.entries)
		out = append(out, l.entries[idx])
	}
	return out
}

func (s *Session) ensureConnectionLog() *ConnectionLog {
	return s.ensureClientManager().ensureConnectionLog()
}

func (s *Session) appendConnectionLog(event, clientID string, cols, rows int, reason string) {
	s.ensureConnectionLog().Append(ConnectionLogEntry{
		Timestamp:        time.Now().UTC(),
		Event:            event,
		ClientID:         clientID,
		Cols:             cols,
		Rows:             rows,
		DisconnectReason: reason,
	})
}
