package server

import "sync/atomic"

type clientManager struct {
	clients       []*clientConn
	connectionLog *ConnectionLog
	sizeClient    atomic.Pointer[clientConn]
	clientCounter atomic.Uint32
}

func newClientManager() *clientManager {
	return &clientManager{
		connectionLog: newConnectionLog(defaultConnectionLogSize),
	}
}

func (s *Session) ensureClientManager() *clientManager {
	if s.clientState == nil {
		s.clientState = newClientManager()
	}
	return s.clientState
}

func (m *clientManager) hasClient(cc *clientConn) bool {
	for _, c := range m.clients {
		if c == cc {
			return true
		}
	}
	return false
}

func (m *clientManager) currentSizeClient() *clientConn {
	return m.sizeClient.Load()
}

func (m *clientManager) noteClientActivity(cc *clientConn) bool {
	if cc == nil || !cc.participatesInSizeNegotiation() || !m.hasClient(cc) || m.currentSizeClient() == cc {
		return false
	}
	m.sizeClient.Store(cc)
	return true
}

func (m *clientManager) effectiveSizeClient() *clientConn {
	if cc := m.currentSizeClient(); cc != nil && cc.participatesInSizeNegotiation() && m.hasClient(cc) {
		return cc
	}
	for i := len(m.clients) - 1; i >= 0; i-- {
		cc := m.clients[i]
		if !cc.participatesInSizeNegotiation() {
			continue
		}
		m.sizeClient.Store(cc)
		return cc
	}
	m.sizeClient.Store(nil)
	return nil
}

func (m *clientManager) removeClient(cc *clientConn) int {
	for i, c := range m.clients {
		if c == cc {
			m.clients = append(m.clients[:i], m.clients[i+1:]...)
			break
		}
	}
	if m.currentSizeClient() == cc {
		m.sizeClient.Store(nil)
	}
	return len(m.clients)
}

func (m *clientManager) addClient(cc *clientConn) {
	m.clients = append(m.clients, cc)
}

func (m *clientManager) snapshotClients() []*clientConn {
	return append([]*clientConn(nil), m.clients...)
}

func (m *clientManager) ensureConnectionLog() *ConnectionLog {
	if m.connectionLog == nil {
		m.connectionLog = newConnectionLog(defaultConnectionLogSize)
	}
	return m.connectionLog
}

func (m *clientManager) nextClientOrdinal() uint32 {
	return m.clientCounter.Add(1)
}

func (m *clientManager) clientCount() int {
	return len(m.clients)
}

func (m *clientManager) firstClient() *clientConn {
	if len(m.clients) == 0 {
		return nil
	}
	return m.clients[0]
}

func (m *clientManager) setClientsForTest(clients ...*clientConn) {
	m.clients = append([]*clientConn(nil), clients...)
}

func (m *clientManager) setSizeOwnerForTest(cc *clientConn) {
	m.sizeClient.Store(cc)
}
