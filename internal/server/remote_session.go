package server

import (
	"fmt"
	"slices"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

type RemoteSessionMode string

const (
	RemoteSessionConnect  RemoteSessionMode = "connect"
	RemoteSessionTakeover RemoteSessionMode = "takeover"
)

type RemoteSession struct {
	Host            string
	Mode            RemoteSessionMode
	State           proto.ConnState
	PlaceholderPane uint32
	RemoteToLocal   map[uint32]uint32
	LocalToRemote   map[uint32]uint32
	WindowByRemote  map[uint32]uint32
}

func NewRemoteSession(host string, mode RemoteSessionMode) *RemoteSession {
	return &RemoteSession{
		Host:           host,
		Mode:           mode,
		State:          proto.Connected,
		RemoteToLocal:  make(map[uint32]uint32),
		LocalToRemote:  make(map[uint32]uint32),
		WindowByRemote: make(map[uint32]uint32),
	}
}

func (rs *RemoteSession) ApplyLayout(s *Session, layout *proto.LayoutSnapshot, activate bool) error {
	if rs == nil {
		return fmt.Errorf("missing remote session")
	}
	if s == nil || layout == nil {
		return fmt.Errorf("missing layout")
	}

	remoteWindows, activeRemoteWindowID := remoteLayoutWindows(layout)
	if len(remoteWindows) == 0 {
		return fmt.Errorf("remote layout for %s has no windows", rs.Host)
	}

	if err := rs.syncProxyPanes(s, remoteWindows); err != nil {
		return err
	}

	switch rs.Mode {
	case RemoteSessionTakeover:
		return rs.applyTakeoverLayout(s, remoteWindows, activeRemoteWindowID)
	default:
		return rs.applyConnectedLayout(s, remoteWindows, activeRemoteWindowID, activate)
	}
}

func (rs *RemoteSession) Remove(s *Session) error {
	if rs == nil || s == nil {
		return nil
	}

	switch rs.Mode {
	case RemoteSessionTakeover:
		if err := rs.removeTakeover(s); err != nil {
			return err
		}
	default:
		rs.removeConnectedWindows(s)
	}

	for _, localPaneID := range rs.sortedLocalPaneIDs() {
		removeRemoteProxyPane(s, localPaneID, "remote disconnect")
	}
	clear(rs.RemoteToLocal)
	clear(rs.LocalToRemote)
	clear(rs.WindowByRemote)
	return nil
}

func (rs *RemoteSession) applyConnectedLayout(s *Session, remoteWindows []proto.WindowSnapshot, activeRemoteWindowID uint32, activate bool) error {
	width, height := remoteLayoutSize(s)

	presentWindows := make(map[uint32]struct{}, len(remoteWindows))
	for _, remoteWin := range remoteWindows {
		presentWindows[remoteWin.ID] = struct{}{}

		paneMap := rs.windowPaneMap(s, remoteWin)
		if len(paneMap) == 0 {
			continue
		}

		rebuilt := mux.RebuildWindowFromSnapshot(remoteWin, width, height, paneMap)
		localWindowID, ok := rs.WindowByRemote[remoteWin.ID]
		var localWindow *mux.Window
		if ok {
			localWindow = s.windowByID(localWindowID)
		}
		if localWindow == nil {
			localWindow = rebuilt
			localWindow.ID = s.windowCounter.Add(1)
			localWindow.Name = remoteWindowName(rs.Host, remoteWin.Name)
			if err := localWindow.ApplyLayout(rebuilt.Root, rebuilt.ActivePane, width, height, remoteWin.ZoomedPaneID, remoteWin.LeadPaneID); err != nil {
				return err
			}
			s.Windows = append(s.Windows, localWindow)
			rs.WindowByRemote[remoteWin.ID] = localWindow.ID
		} else {
			localWindow.Name = remoteWindowName(rs.Host, remoteWin.Name)
			if err := localWindow.ApplyLayout(rebuilt.Root, rebuilt.ActivePane, width, height, remoteWin.ZoomedPaneID, remoteWin.LeadPaneID); err != nil {
				return err
			}
		}
	}

	for remoteWindowID, localWindowID := range rs.WindowByRemote {
		if _, ok := presentWindows[remoteWindowID]; ok {
			continue
		}
		s.removeWindow(localWindowID)
		delete(rs.WindowByRemote, remoteWindowID)
	}

	if activate {
		localWindowID := rs.WindowByRemote[activeRemoteWindowID]
		if localWindow := s.windowByID(localWindowID); localWindow != nil {
			s.activateWindow(localWindow)
		}
	}
	return nil
}

func (rs *RemoteSession) applyTakeoverLayout(s *Session, remoteWindows []proto.WindowSnapshot, activeRemoteWindowID uint32) error {
	sshPane := s.findPaneByID(rs.PlaceholderPane)
	if sshPane == nil {
		return fmt.Errorf("takeover pane %d not found", rs.PlaceholderPane)
	}

	activeWindow := remoteWindows[0]
	for _, remoteWin := range remoteWindows {
		if remoteWin.ID == activeRemoteWindowID {
			activeWindow = remoteWin
			break
		}
	}

	window := s.findWindowByPaneID(rs.PlaceholderPane)
	if window == nil {
		for _, candidate := range s.Windows {
			if candidate == nil {
				continue
			}
			found := false
			for _, pane := range candidate.Panes() {
				if pane != nil && pane.Meta.Host == rs.Host && pane.IsProxy() {
					found = true
					break
				}
			}
			if found {
				window = candidate
				break
			}
		}
	}
	if window == nil {
		return fmt.Errorf("takeover host %s not in any window", rs.Host)
	}

	if paneCell := window.Root.FindPane(rs.PlaceholderPane); paneCell == nil {
		if err := window.UnsplicePane(rs.Host, sshPane); err != nil {
			return err
		}
	}

	subtree, activePane := rs.remoteSubtree(s, activeWindow)
	if subtree == nil {
		return fmt.Errorf("remote host %s active window has no mapped panes", rs.Host)
	}

	if err := window.SplicePaneWithLayout(rs.PlaceholderPane, subtree, activePane); err != nil {
		return err
	}
	sshPane.Meta.Dormant = true
	return nil
}

func (rs *RemoteSession) removeConnectedWindows(s *Session) {
	for _, localWindowID := range rs.sortedLocalWindowIDs() {
		s.removeWindow(localWindowID)
	}
	clear(rs.WindowByRemote)
}

func (rs *RemoteSession) removeTakeover(s *Session) error {
	sshPane := s.findPaneByID(rs.PlaceholderPane)
	if sshPane == nil {
		return nil
	}
	window := s.findWindowByPaneID(rs.PlaceholderPane)
	if window == nil {
		for _, candidate := range s.Windows {
			if candidate != nil {
				if err := candidate.UnsplicePane(rs.Host, sshPane); err == nil {
					sshPane.Meta.Dormant = false
					return nil
				}
			}
		}
		return nil
	}
	if err := window.UnsplicePane(rs.Host, sshPane); err != nil {
		return err
	}
	sshPane.Meta.Dormant = false
	return nil
}

func (rs *RemoteSession) syncProxyPanes(s *Session, remoteWindows []proto.WindowSnapshot) error {
	presentRemote := make(map[uint32]proto.PaneSnapshot)
	for _, remoteWin := range remoteWindows {
		for _, pane := range remoteWin.Panes {
			presentRemote[pane.ID] = pane
		}
	}

	for remotePaneID, pane := range presentRemote {
		localPaneID, ok := rs.RemoteToLocal[remotePaneID]
		if !ok {
			localPane, err := rs.newProxyPane(s, pane)
			if err != nil {
				return err
			}
			s.Panes = append(s.Panes, localPane)
			s.appendPaneLog(paneLogEventCreate, localPane, "")
			s.logPaneCreate(localPane, "remote-session")
			localPaneID = localPane.ID
			rs.RemoteToLocal[remotePaneID] = localPaneID
			rs.LocalToRemote[localPaneID] = remotePaneID
			if s.RemoteManager != nil {
				if err := s.RemoteManager.RegisterPane(rs.Host, localPaneID, remotePaneID); err != nil {
					return err
				}
			}
		}
		if localPane := s.findPaneByID(localPaneID); localPane != nil {
			rs.applyPaneSnapshot(localPane, pane)
		}
	}

	for remotePaneID, localPaneID := range rs.RemoteToLocal {
		if _, ok := presentRemote[remotePaneID]; ok {
			continue
		}
		removeRemoteProxyPane(s, localPaneID, "remote layout changed")
		delete(rs.RemoteToLocal, remotePaneID)
		delete(rs.LocalToRemote, localPaneID)
	}
	return nil
}

func (rs *RemoteSession) newProxyPane(s *Session, pane proto.PaneSnapshot) (*mux.Pane, error) {
	localPaneID := s.counter.Add(1)
	meta := mux.PaneMeta{
		Name:   pane.Name,
		Host:   rs.Host,
		Task:   pane.Task,
		Color:  s.remotePaneColor(rs.Host),
		Remote: string(rs.State),
	}
	proxy := s.ownPane(mux.NewProxyPaneWithScrollback(
		localPaneID,
		meta,
		DefaultTermCols,
		mux.PaneContentHeight(DefaultTermRows),
		s.scrollbackLines,
		s.paneOutputCallback(),
		s.paneExitCallback(),
		s.remoteWriteOverride(localPaneID),
	))
	rs.applyPaneSnapshot(proxy, pane)
	return proxy, nil
}

func (rs *RemoteSession) applyPaneSnapshot(localPane *mux.Pane, remotePane proto.PaneSnapshot) {
	if localPane == nil {
		return
	}
	localPane.Meta.Name = remotePane.Name
	localPane.Meta.Host = rs.Host
	localPane.Meta.Task = remotePane.Task
	localPane.Meta.GitBranch = remotePane.GitBranch
	localPane.Meta.PR = remotePane.PR
	localPane.Meta.TrackedPRs = proto.CloneTrackedPRs(remotePane.TrackedPRs)
	localPane.Meta.TrackedIssues = proto.CloneTrackedIssues(remotePane.TrackedIssues)
	localPane.Meta.KV = mux.CloneMetaKV(remotePane.KV)
	localPane.Meta.Remote = string(rs.State)
}

func (rs *RemoteSession) remoteSubtree(s *Session, remoteWin proto.WindowSnapshot) (*mux.LayoutCell, *mux.Pane) {
	paneMap := rs.windowPaneMap(s, remoteWin)
	if len(paneMap) == 0 {
		return nil, nil
	}
	rebuilt := mux.RebuildWindowFromSnapshot(remoteWin, remoteWin.Root.W, remoteWin.Root.H, paneMap)
	return rebuilt.Root, rebuilt.ActivePane
}

func (rs *RemoteSession) windowPaneMap(s *Session, remoteWin proto.WindowSnapshot) map[uint32]*mux.Pane {
	paneMap := make(map[uint32]*mux.Pane, len(remoteWin.Panes))
	for _, pane := range remoteWin.Panes {
		localPaneID, ok := rs.RemoteToLocal[pane.ID]
		if !ok {
			continue
		}
		if localPane := s.findPaneByID(localPaneID); localPane != nil {
			paneMap[pane.ID] = localPane
		}
	}
	return paneMap
}

func (rs *RemoteSession) sortedLocalPaneIDs() []uint32 {
	ids := make([]uint32, 0, len(rs.LocalToRemote))
	for localPaneID := range rs.LocalToRemote {
		ids = append(ids, localPaneID)
	}
	slices.Sort(ids)
	return ids
}

func (rs *RemoteSession) sortedLocalWindowIDs() []uint32 {
	ids := make([]uint32, 0, len(rs.WindowByRemote))
	for _, localWindowID := range rs.WindowByRemote {
		ids = append(ids, localWindowID)
	}
	slices.Sort(ids)
	return ids
}

func remoteLayoutWindows(layout *proto.LayoutSnapshot) ([]proto.WindowSnapshot, uint32) {
	if layout == nil {
		return nil, 0
	}
	if len(layout.Windows) > 0 {
		return append([]proto.WindowSnapshot(nil), layout.Windows...), layout.ActiveWindowID
	}
	windowID := layout.ActiveWindowID
	if windowID == 0 {
		windowID = 1
	}
	return []proto.WindowSnapshot{{
		ID:           windowID,
		Name:         "window-1",
		Index:        1,
		ActivePaneID: layout.ActivePaneID,
		ZoomedPaneID: layout.ZoomedPaneID,
		LeadPaneID:   layout.LeadPaneID,
		Root:         layout.Root,
		Panes:        append([]proto.PaneSnapshot(nil), layout.Panes...),
	}}, windowID
}

func remoteLayoutSize(s *Session) (int, int) {
	if s == nil {
		return DefaultTermCols, DefaultTermRows
	}
	if w := s.activeWindow(); w != nil {
		return w.Width, w.Height
	}
	return DefaultTermCols, DefaultTermRows
}

func remoteWindowName(hostName, windowName string) string {
	if windowName == "" {
		return hostName
	}
	return fmt.Sprintf("%s@%s", windowName, hostName)
}

func removeRemoteProxyPane(s *Session, localPaneID uint32, reason string) {
	if s == nil {
		return
	}
	removed := s.finalizePaneRemoval(localPaneID)
	if removed.pane == nil {
		return
	}
	s.appendPaneLog(paneLogEventExit, removed.pane, reason)
	s.emitEvent(Event{
		Type:     EventPaneExit,
		PaneID:   localPaneID,
		PaneName: removed.paneName,
		Host:     removed.pane.Meta.Host,
		Reason:   reason,
	})
	s.logPaneExit(removed.pane, reason)
	s.closePaneAsync(removed.pane)
}

func (s *Session) connectRemoteSession(hostName string, layout *proto.LayoutSnapshot, mode RemoteSessionMode, placeholderPaneID uint32, activate bool) error {
	if s == nil {
		return fmt.Errorf("no session")
	}
	rs := s.remoteSessions[hostName]
	if rs == nil {
		rs = NewRemoteSession(hostName, mode)
		s.remoteSessions[hostName] = rs
	}
	rs.Mode = mode
	if placeholderPaneID != 0 {
		rs.PlaceholderPane = placeholderPaneID
	}
	if s.RemoteManager != nil {
		rs.State = s.RemoteManager.HostStatus(hostName)
	}
	return rs.ApplyLayout(s, layout, activate)
}

func (s *Session) disconnectRemoteSession(hostName string) error {
	if s == nil {
		return fmt.Errorf("no session")
	}
	rs := s.remoteSessions[hostName]
	if rs == nil {
		return nil
	}
	if err := rs.Remove(s); err != nil {
		return err
	}
	delete(s.remoteSessions, hostName)
	return nil
}
