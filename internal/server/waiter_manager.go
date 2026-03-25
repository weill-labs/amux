package server

import (
	"sync/atomic"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

type layoutWaiter struct {
	afterGen uint64
	reply    chan uint64
}

type clipboardWaiter struct {
	afterGen uint64
	reply    chan string
}

type hookWaiter struct {
	afterGen  uint64
	eventName string
	paneID    uint32
	paneName  string
	reply     chan hookResultRecord
}

type paneOutputWaitStart struct {
	ch      chan struct{}
	matched bool
	exists  bool
}

type waiterManager struct {
	waiterCounter atomic.Uint64

	layoutWaiters map[uint64]layoutWaiter

	paneOutputSubs map[uint32][]chan struct{}

	clipboardGen     atomic.Uint64
	lastClipboardB64 string
	clipboardWaiters map[uint64]clipboardWaiter

	hookGen     atomic.Uint64
	hookResults []hookResultRecord
	hookWaiters map[uint64]hookWaiter
}

func newWaiterManager() *waiterManager {
	return &waiterManager{
		layoutWaiters:    make(map[uint64]layoutWaiter),
		paneOutputSubs:   make(map[uint32][]chan struct{}),
		clipboardWaiters: make(map[uint64]clipboardWaiter),
		hookWaiters:      make(map[uint64]hookWaiter),
	}
}

func (s *Session) ensureWaiters() *waiterManager {
	if s.waiters == nil {
		s.waiters = newWaiterManager()
	}
	return s.waiters
}

func (m *waiterManager) clipboardGeneration() uint64 {
	if m == nil {
		return 0
	}
	return m.clipboardGen.Load()
}

func (m *waiterManager) hookGeneration() uint64 {
	if m == nil {
		return 0
	}
	return m.hookGen.Load()
}

func (s *Session) clipboardGeneration() uint64 {
	if s == nil {
		return 0
	}
	return s.ensureWaiters().clipboardGeneration()
}

func (s *Session) hookGeneration() uint64 {
	if s == nil {
		return 0
	}
	return s.ensureWaiters().hookGeneration()
}

func (m *waiterManager) removePane(paneID uint32) {
	if m == nil {
		return
	}
	delete(m.paneOutputSubs, paneID)
}

func (m *waiterManager) addPaneOutputSubscriber(paneID uint32) chan struct{} {
	ch := make(chan struct{}, 1)
	m.paneOutputSubs[paneID] = append(m.paneOutputSubs[paneID], ch)
	return ch
}

func (m *waiterManager) removePaneOutputSubscriber(paneID uint32, ch chan struct{}) {
	subs := m.paneOutputSubs[paneID]
	for i, sub := range subs {
		if sub == ch {
			m.paneOutputSubs[paneID] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
}

func (m *waiterManager) notifyPaneOutputSubs(paneID uint32) {
	for _, ch := range m.paneOutputSubs[paneID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (m *waiterManager) beginPaneOutputWait(sess *Session, paneID uint32, substr string) (paneOutputWaitStart, error) {
	return enqueueSessionQuery(sess, func(s *Session) (paneOutputWaitStart, error) {
		pane := s.findPaneByID(paneID)
		if pane == nil {
			return paneOutputWaitStart{}, nil
		}
		ch := m.addPaneOutputSubscriber(paneID)
		return paneOutputWaitStart{
			ch:      ch,
			matched: pane.ScreenContains(substr),
			exists:  true,
		}, nil
	})
}

func (m *waiterManager) notifyLayoutWaiters(gen uint64) {
	for id, waiter := range m.layoutWaiters {
		if gen <= waiter.afterGen {
			continue
		}
		waiter.reply <- gen
		delete(m.layoutWaiters, id)
	}
}

func (m *waiterManager) waitGeneration(sess *Session, afterGen uint64, timeout time.Duration) (uint64, bool) {
	type waitRegistration struct {
		gen      uint64
		waiterID uint64
		reply    chan uint64
	}
	type waitState struct {
		gen     uint64
		matched bool
	}

	reg, err := enqueueSessionQuery(sess, func(s *Session) (waitRegistration, error) {
		gen := s.generation.Load()
		if gen > afterGen {
			return waitRegistration{gen: gen}, nil
		}
		reply := make(chan uint64, 1)
		waiterID := m.waiterCounter.Add(1)
		m.layoutWaiters[waiterID] = layoutWaiter{afterGen: afterGen, reply: reply}
		return waitRegistration{waiterID: waiterID, reply: reply}, nil
	})
	if err != nil {
		return 0, false
	}
	if reg.reply == nil {
		return reg.gen, true
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case gen := <-reg.reply:
		return gen, true
	case <-timer.C:
		state, err := enqueueSessionQuery(sess, func(s *Session) (waitState, error) {
			delete(m.layoutWaiters, reg.waiterID)
			gen := s.generation.Load()
			return waitState{gen: gen, matched: gen > afterGen}, nil
		})
		if err != nil {
			return 0, false
		}
		return state.gen, state.matched
	}
}

func (m *waiterManager) waitGenerationAfterCurrent(sess *Session, timeout time.Duration) (uint64, bool) {
	afterGen, err := enqueueSessionQuery(sess, func(s *Session) (uint64, error) {
		return s.generation.Load(), nil
	})
	if err != nil {
		return 0, false
	}
	return m.waitGeneration(sess, afterGen, timeout)
}

func (m *waiterManager) recordClipboard(data []byte) (uint64, string) {
	m.lastClipboardB64 = string(data)
	gen := m.clipboardGen.Add(1)
	m.notifyClipboardWaiters(gen, m.lastClipboardB64)
	return gen, m.lastClipboardB64
}

func (m *waiterManager) notifyClipboardWaiters(gen uint64, payload string) {
	for id, waiter := range m.clipboardWaiters {
		if gen <= waiter.afterGen {
			continue
		}
		waiter.reply <- payload
		delete(m.clipboardWaiters, id)
	}
}

func (m *waiterManager) waitClipboard(sess *Session, afterGen uint64, timeout time.Duration) (string, bool) {
	type waitRegistration struct {
		payload  string
		waiterID uint64
		reply    chan string
	}
	type waitState struct {
		payload string
		matched bool
	}

	reg, err := enqueueSessionQuery(sess, func(s *Session) (waitRegistration, error) {
		gen := m.clipboardGen.Load()
		if gen > afterGen {
			return waitRegistration{payload: m.lastClipboardB64}, nil
		}
		reply := make(chan string, 1)
		waiterID := m.waiterCounter.Add(1)
		m.clipboardWaiters[waiterID] = clipboardWaiter{afterGen: afterGen, reply: reply}
		return waitRegistration{waiterID: waiterID, reply: reply}, nil
	})
	if err != nil {
		return "", false
	}
	if reg.reply == nil {
		return reg.payload, true
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case payload := <-reg.reply:
		return payload, true
	case <-timer.C:
		state, err := enqueueSessionQuery(sess, func(s *Session) (waitState, error) {
			delete(m.clipboardWaiters, reg.waiterID)
			if m.clipboardGen.Load() > afterGen {
				return waitState{payload: m.lastClipboardB64, matched: true}, nil
			}
			return waitState{}, nil
		})
		if err != nil {
			return "", false
		}
		return state.payload, state.matched
	}
}

func (m *waiterManager) waitClipboardAfterCurrent(sess *Session, timeout time.Duration) (string, bool) {
	afterGen, err := enqueueSessionQuery(sess, func(s *Session) (uint64, error) {
		return m.clipboardGen.Load(), nil
	})
	if err != nil {
		return "", false
	}
	return m.waitClipboard(sess, afterGen, timeout)
}

func (m *waiterManager) matchHookResult(afterGen uint64, eventName string, paneID uint32, paneName string) (hookResultRecord, bool) {
	for _, record := range m.hookResults {
		if record.Generation <= afterGen {
			continue
		}
		if eventName != "" && record.Event != eventName {
			continue
		}
		if paneID != 0 && record.PaneID != 0 && record.PaneID != paneID {
			continue
		}
		if paneName != "" && record.PaneName != paneName {
			continue
		}
		return record, true
	}
	return hookResultRecord{}, false
}

func (m *waiterManager) notifyHookWaiters(record hookResultRecord) {
	for id, waiter := range m.hookWaiters {
		if record.Generation <= waiter.afterGen {
			continue
		}
		if waiter.eventName != "" && record.Event != waiter.eventName {
			continue
		}
		if waiter.paneID != 0 && record.PaneID != 0 && record.PaneID != waiter.paneID {
			continue
		}
		if waiter.paneName != "" && record.PaneName != waiter.paneName {
			continue
		}
		waiter.reply <- record
		delete(m.hookWaiters, id)
	}
}

func (m *waiterManager) appendHookResult(record hookResultRecord) hookResultRecord {
	record.Generation = m.hookGen.Add(1)
	m.hookResults = append(m.hookResults, record)
	if len(m.hookResults) > 128 {
		m.hookResults = append([]hookResultRecord(nil), m.hookResults[len(m.hookResults)-128:]...)
	}
	m.notifyHookWaiters(record)
	return record
}

func (m *waiterManager) waitHookForPaneAfterCurrent(sess *Session, eventName string, paneID uint32, paneName string, timeout time.Duration) (hookResultRecord, bool) {
	afterGen, err := enqueueSessionQuery(sess, func(s *Session) (uint64, error) {
		return m.hookGen.Load(), nil
	})
	if err != nil {
		return hookResultRecord{}, false
	}
	return m.waitHookForPane(sess, afterGen, eventName, paneID, paneName, timeout)
}

func (m *waiterManager) waitHookForPane(sess *Session, afterGen uint64, eventName string, paneID uint32, paneName string, timeout time.Duration) (hookResultRecord, bool) {
	type waitRegistration struct {
		record   hookResultRecord
		waiterID uint64
		reply    chan hookResultRecord
	}
	type waitState struct {
		record  hookResultRecord
		matched bool
	}

	reg, err := enqueueSessionQuery(sess, func(s *Session) (waitRegistration, error) {
		if record, ok := m.matchHookResult(afterGen, eventName, paneID, paneName); ok {
			return waitRegistration{record: record}, nil
		}
		reply := make(chan hookResultRecord, 1)
		waiterID := m.waiterCounter.Add(1)
		m.hookWaiters[waiterID] = hookWaiter{
			afterGen:  afterGen,
			eventName: eventName,
			paneID:    paneID,
			paneName:  paneName,
			reply:     reply,
		}
		return waitRegistration{waiterID: waiterID, reply: reply}, nil
	})
	if err != nil {
		return hookResultRecord{}, false
	}
	if reg.reply == nil {
		return reg.record, true
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case record := <-reg.reply:
		return record, true
	case <-timer.C:
		state, err := enqueueSessionQuery(sess, func(s *Session) (waitState, error) {
			delete(m.hookWaiters, reg.waiterID)
			record, ok := m.matchHookResult(afterGen, eventName, paneID, paneName)
			return waitState{record: record, matched: ok}, nil
		})
		if err != nil {
			return hookResultRecord{}, false
		}
		return state.record, state.matched
	}
}

func (m *waiterManager) waitHook(sess *Session, afterGen uint64, eventName, paneName string, timeout time.Duration) (hookResultRecord, bool) {
	return m.waitHookForPane(sess, afterGen, eventName, 0, paneName, timeout)
}

func (m *waiterManager) outputSubscriberCount(paneID uint32) int {
	if m == nil {
		return 0
	}
	return len(m.paneOutputSubs[paneID])
}

func (m *waiterManager) paneOutputWaiterRegistered(paneID uint32) bool {
	if m == nil {
		return false
	}
	return len(m.paneOutputSubs[paneID]) > 0
}

func (m *waiterManager) layoutWaiterRegistered(afterGen uint64) bool {
	for _, waiter := range m.layoutWaiters {
		if waiter.afterGen == afterGen {
			return true
		}
	}
	return false
}

func (m *waiterManager) clipboardWaiterRegistered(afterGen uint64) bool {
	for _, waiter := range m.clipboardWaiters {
		if waiter.afterGen == afterGen {
			return true
		}
	}
	return false
}

func (m *waiterManager) hookWaiterRegistered(afterGen uint64, eventName string, paneID uint32, paneName string) bool {
	for _, waiter := range m.hookWaiters {
		if waiter.afterGen == afterGen && waiter.eventName == eventName && waiter.paneID == paneID && waiter.paneName == paneName {
			return true
		}
	}
	return false
}

func (m *waiterManager) retainedHookResults() []hookResultRecord {
	return append([]hookResultRecord(nil), m.hookResults...)
}

func (m *waiterManager) setClipboardStateForTest(gen uint64, payload string) {
	m.clipboardGen.Store(gen)
	m.lastClipboardB64 = payload
}

func (m *waiterManager) setHookStateForTest(gen uint64, results []hookResultRecord) {
	m.hookGen.Store(gen)
	m.hookResults = append([]hookResultRecord(nil), results...)
}

func (m *waiterManager) paneExistsAndMatches(pane *mux.Pane, substr string) paneOutputWaitStart {
	if pane == nil {
		return paneOutputWaitStart{}
	}
	return paneOutputWaitStart{
		matched: pane.ScreenContains(substr),
		exists:  true,
	}
}
