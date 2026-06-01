package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

const paneMetaUpdateAfterInputDelay = 150 * time.Millisecond

func (s *Session) enqueueMirrorPaneMetaUpdate(paneID uint32, update proto.PaneMetaUpdate) {
	if s == nil || s.shutdown.Load() {
		return
	}
	s.enqueueEvent(s.context(), mirrorPaneMetaUpdateEvent{paneID: paneID, update: update})
}

type mirrorPaneMetaUpdateEvent struct {
	paneID uint32
	update proto.PaneMetaUpdate
}

func (e mirrorPaneMetaUpdateEvent) handle(_ context.Context, s *Session) {
	pane := s.findPaneByID(e.paneID)
	if pane == nil {
		return
	}
	applyForwardedPaneMetaUpdate(pane, e.update)
	s.broadcastLayoutNow()
}

func applyForwardedPaneMetaUpdate(pane *mux.Pane, update proto.PaneMetaUpdate) {
	if pane == nil {
		return
	}
	pane.Meta.GitBranch = update.GitBranch
	pane.Meta.PR = update.PR
	pane.Meta.TrackedPRs = proto.CloneTrackedPRs(update.TrackedPRs)
	pane.Meta.TrackedIssues = proto.CloneTrackedIssues(update.TrackedIssues)

	setForwardedMetaKV(&pane.Meta, mux.PaneMetaKeyBranch, update.GitBranch)
	setForwardedMetaKV(&pane.Meta, mux.PaneMetaKeyPR, update.PR)
	setForwardedMetaJSONKV(&pane.Meta, mux.PaneMetaKeyTrackedPRs, update.TrackedPRs)
	setForwardedMetaJSONKV(&pane.Meta, mux.PaneMetaKeyTrackedIssues, update.TrackedIssues)
}

func setForwardedMetaKV(meta *mux.PaneMeta, key, value string) {
	if meta == nil {
		return
	}
	if value == "" {
		deleteForwardedMetaKV(meta, key)
		return
	}
	if meta.KV == nil {
		meta.KV = map[string]string{}
	}
	meta.KV[key] = value
}

func setForwardedMetaJSONKV[T any](meta *mux.PaneMeta, key string, values []T) {
	if len(values) == 0 {
		deleteForwardedMetaKV(meta, key)
		return
	}
	data, err := json.Marshal(values)
	if err != nil {
		deleteForwardedMetaKV(meta, key)
		return
	}
	setForwardedMetaKV(meta, key, string(data))
}

func deleteForwardedMetaKV(meta *mux.PaneMeta, key string) {
	if meta == nil || meta.KV == nil {
		return
	}
	delete(meta.KV, key)
	if len(meta.KV) == 0 {
		meta.KV = nil
	}
}

func (s *Session) broadcastPaneMetaUpdate(paneID uint32) {
	_, _ = enqueueSessionQueryOnState(s.context(), s, func(s *Session) (struct{}, error) {
		s.broadcastPaneMetaUpdateNow(paneID)
		return struct{}{}, nil
	})
}

func (s *Session) schedulePaneMetaUpdateAfterInput(paneID uint32) {
	time.AfterFunc(paneMetaUpdateAfterInputDelay, func() {
		s.broadcastPaneMetaUpdate(paneID)
	})
}

func (s *Session) broadcastScopedPaneMetaUpdatesNow() {
	seen := map[uint32]struct{}{}
	for _, cc := range s.ensureClientManager().snapshotClients() {
		if !cc.isPaneScoped() || cc.isBootstrapping() {
			continue
		}
		if _, ok := seen[cc.scopedPaneID]; ok {
			continue
		}
		seen[cc.scopedPaneID] = struct{}{}
		s.broadcastPaneMetaUpdateNow(cc.scopedPaneID)
	}
}

func (s *Session) broadcastPaneMetaUpdateNow(paneID uint32) {
	clients := s.scopedPaneClientsNow(paneID)
	if len(clients) == 0 {
		return
	}
	pane := s.findPaneByID(paneID)
	if pane == nil {
		return
	}
	msg := s.paneMetaUpdateMessage(pane)
	for _, cc := range clients {
		_ = cc.Send(msg)
	}
}

func (s *Session) scopedPaneClientsNow(paneID uint32) []*clientConn {
	var clients []*clientConn
	for _, cc := range s.ensureClientManager().snapshotClients() {
		if cc.isScopedToPane(paneID) && !cc.isBootstrapping() {
			clients = append(clients, cc)
		}
	}
	return clients
}

func (s *Session) hasScopedPaneClientsNow(paneID uint32) bool {
	for _, cc := range s.ensureClientManager().snapshotClients() {
		if cc.isScopedToPane(paneID) && !cc.isBootstrapping() {
			return true
		}
	}
	return false
}

func (s *Session) paneMetaUpdateMessage(pane *mux.Pane) *Message {
	return &Message{
		Type:           MsgTypePaneMetaUpdate,
		PaneID:         pane.ID,
		PaneMetaUpdate: s.paneMetaUpdate(pane),
	}
}

func (s *Session) paneMetaUpdate(pane *mux.Pane) *proto.PaneMetaUpdate {
	update := &proto.PaneMetaUpdate{
		GitBranch:     pane.Meta.GitBranch,
		PR:            pane.Meta.PR,
		TrackedPRs:    proto.CloneTrackedPRs(pane.Meta.TrackedPRs),
		TrackedIssues: proto.CloneTrackedIssues(pane.Meta.TrackedIssues),
	}
	if status, ok := s.forwardedAgentStatusForPane(pane); ok {
		update.AgentStatus = status
		return update
	}
	_, sinceSnap := s.snapshotIdleFull()
	update.AgentStatus = s.paneAgentStatusFromState(pane, pane.AgentStatus(), sinceSnap, s.clock().Now())
	return update
}

func (s *Session) forwardedAgentStatusForPane(pane *mux.Pane) (proto.PaneAgentStatus, bool) {
	if s == nil || pane == nil || !pane.IsProxy() || s.mirror == nil {
		return proto.PaneAgentStatus{}, false
	}
	return s.mirror.AgentStatus(pane.ID)
}

func (s *Session) captureMirrorForPane(paneID uint32) *proto.CaptureMirror {
	if s == nil || s.mirror == nil {
		return nil
	}
	snap, ok := s.mirror.Snapshot(paneID)
	if !ok {
		return nil
	}
	return &proto.CaptureMirror{
		State:        string(snap.State),
		Host:         snap.RemoteRef.Host,
		Session:      snap.RemoteRef.Session,
		PaneName:     snap.RemoteRef.PaneName,
		RemotePaneID: snap.RemotePaneID,
		LastError:    snap.LastError,
	}
}
