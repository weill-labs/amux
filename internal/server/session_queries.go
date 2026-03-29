package server

import (
	"fmt"
	"strings"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

type activeWindowSnapshot struct {
	activePID int
	width     int
	height    int
	proxyHost string
}

type resolvedPaneRef struct {
	pane     *mux.Pane
	window   *mux.Window
	paneID   uint32
	paneName string
	windowID uint32
}

type killTargetSnapshot struct {
	paneID   uint32
	paneName string
	proxy    bool
}

type paneListEntry struct {
	paneID     uint32
	name       string
	host       string
	windowName string
	task       string
	cwd        string
	gitBranch  string
	idle       string
	pr         string
	prs        []proto.TrackedPR
	issues     []proto.TrackedIssue
	active     bool
	lead       bool
}

type sessionStatusSnapshot struct {
	total       int
	windowCount int
	zoomed      string
}

type windowListEntry struct {
	index     int
	name      string
	paneCount int
	active    bool
}

type clientListEntry struct {
	id           string
	displayPanes string
	chooser      string
	size         string
	sizeOwner    bool
	capabilities string
}

type uiClientSnapshot struct {
	client       *clientConn
	clientID     string
	currentMatch bool
	currentGen   uint64
}

type actorPaneContext struct {
	window *mux.Window
}

// resolveUIClientSnapshot must run on the session event loop. It resolves the
// target client and snapshots whether it already matches the requested UI
// event, so callers can combine resolution with subscription atomically.
func (s *Session) resolveUIClientSnapshot(requestedClientID, eventName string) (uiClientSnapshot, error) {
	clients := s.ensureClientManager().clients
	if len(clients) == 0 {
		return uiClientSnapshot{}, fmt.Errorf("no client attached")
	}
	if requestedClientID != "" {
		for _, cc := range clients {
			if cc.ID == requestedClientID {
				currentMatch := false
				if eventName != "" {
					currentMatch = cc.matchesUIEvent(eventName)
				}
				return uiClientSnapshot{
					client:       cc,
					clientID:     cc.ID,
					currentMatch: currentMatch,
					currentGen:   cc.uiGeneration,
				}, nil
			}
		}
		return uiClientSnapshot{}, fmt.Errorf("unknown client: %s", requestedClientID)
	}
	if len(clients) == 1 {
		cc := clients[0]
		currentMatch := false
		if eventName != "" {
			currentMatch = cc.matchesUIEvent(eventName)
		}
		return uiClientSnapshot{
			client:       cc,
			clientID:     cc.ID,
			currentMatch: currentMatch,
			currentGen:   cc.uiGeneration,
		}, nil
	}
	ids := make([]string, 0, len(clients))
	for _, cc := range clients {
		ids = append(ids, cc.ID)
	}
	return uiClientSnapshot{}, fmt.Errorf("multiple clients attached; specify --client (%s)", strings.Join(ids, ", "))
}

func (s *Session) actorPaneContext(actorPaneID uint32) actorPaneContext {
	if actorPaneID == 0 {
		return actorPaneContext{}
	}
	window := s.findWindowByPaneID(actorPaneID)
	if window == nil {
		return actorPaneContext{}
	}
	return actorPaneContext{window: window}
}

func appendUniqueWindow(windows []*mux.Window, seen map[uint32]struct{}, window *mux.Window) []*mux.Window {
	if window == nil {
		return windows
	}
	if _, ok := seen[window.ID]; ok {
		return windows
	}
	seen[window.ID] = struct{}{}
	return append(windows, window)
}

func (s *Session) explicitPaneSearchWindows(actorPaneID uint32) []*mux.Window {
	seen := make(map[uint32]struct{}, len(s.Windows))
	var windows []*mux.Window

	actor := s.actorPaneContext(actorPaneID)
	windows = appendUniqueWindow(windows, seen, actor.window)
	windows = appendUniqueWindow(windows, seen, s.activeWindow())
	for _, window := range s.Windows {
		windows = appendUniqueWindow(windows, seen, window)
	}
	return windows
}

func (s *Session) resolvePaneAcrossWindowsForActor(actorPaneID uint32, ref string) (*mux.Pane, *mux.Window, error) {
	windows := s.explicitPaneSearchWindows(actorPaneID)
	if len(windows) == 0 {
		return nil, nil, fmt.Errorf("no session")
	}

	seen := make(map[uint32]struct{}, len(s.Panes))
	for _, window := range windows {
		panes := window.Panes()
		candidates := make([]mux.PaneRefCandidate, 0, len(panes))
		byID := make(map[uint32]*mux.Pane, len(panes))
		for _, pane := range panes {
			seen[pane.ID] = struct{}{}
			candidates = append(candidates, mux.PaneRefCandidate{ID: pane.ID, Name: pane.Meta.Name})
			byID[pane.ID] = pane
		}
		paneID, err := mux.ResolvePaneRef(ref, candidates)
		switch {
		case err == nil:
			return byID[paneID], window, nil
		case err.Error() == fmt.Sprintf("pane %q not found", ref):
			continue
		default:
			return nil, nil, err
		}
	}

	candidates := make([]mux.PaneRefCandidate, 0, len(s.Panes))
	byID := make(map[uint32]*mux.Pane, len(s.Panes))
	for _, pane := range s.Panes {
		if _, ok := seen[pane.ID]; ok {
			continue
		}
		candidates = append(candidates, mux.PaneRefCandidate{ID: pane.ID, Name: pane.Meta.Name})
		byID[pane.ID] = pane
	}

	paneID, err := mux.ResolvePaneRef(ref, candidates)
	if err != nil {
		return nil, nil, err
	}
	pane := byID[paneID]
	return pane, s.findWindowByPaneID(paneID), nil
}

func (s *Session) resolvePaneWindowForActor(actorPaneID uint32, cmdName string, args []string) (*mux.Pane, *mux.Window, error) {
	if len(args) < 1 {
		return nil, nil, fmt.Errorf("usage: %s <pane>", cmdName)
	}
	pane, w, err := s.resolvePaneAcrossWindowsForActor(actorPaneID, args[0])
	if err != nil {
		return nil, nil, err
	}
	if w == nil {
		return nil, nil, fmt.Errorf("pane not in any window")
	}
	return pane, w, nil
}

func (s *Session) windowForActor(actorPaneID uint32) *mux.Window {
	actor := s.actorPaneContext(actorPaneID)
	if actor.window != nil {
		return actor.window
	}
	return s.activeWindow()
}

func (s *Session) queryActiveWindowSnapshot() (activeWindowSnapshot, error) {
	return enqueueSessionQuery(s, func(s *Session) (activeWindowSnapshot, error) {
		w := s.activeWindow()
		if w == nil {
			return activeWindowSnapshot{}, fmt.Errorf("no window")
		}
		snap := activeWindowSnapshot{
			width:  w.Width,
			height: w.Height,
		}
		if w.ActivePane != nil {
			snap.activePID = w.ActivePane.ProcessPid()
			if w.ActivePane.IsProxy() {
				snap.proxyHost = w.ActivePane.Meta.Host
			}
		}
		return snap, nil
	})
}

func (s *Session) queryResolvedPaneForActor(actorPaneID uint32, ref string) (resolvedPaneRef, error) {
	return enqueueSessionQuery(s, func(s *Session) (resolvedPaneRef, error) {
		pane, w, err := s.resolvePaneAcrossWindowsForActor(actorPaneID, ref)
		if err != nil {
			return resolvedPaneRef{}, err
		}
		snap := resolvedPaneRef{
			pane:     pane,
			window:   w,
			paneID:   pane.ID,
			paneName: pane.Meta.Name,
		}
		if w != nil {
			snap.windowID = w.ID
		}
		return snap, nil
	})
}

func (s *Session) queryKillTarget(actorPaneID uint32, ref string) (killTargetSnapshot, error) {
	return enqueueSessionQuery(s, func(s *Session) (killTargetSnapshot, error) {
		var pane *mux.Pane
		if ref == "" {
			w := s.windowForActor(actorPaneID)
			if w == nil || w.ActivePane == nil {
				return killTargetSnapshot{}, nil
			}
			pane = w.ActivePane
		} else {
			var err error
			pane, _, err = s.resolvePaneAcrossWindowsForActor(actorPaneID, ref)
			if err != nil {
				return killTargetSnapshot{}, err
			}
		}
		return killTargetSnapshot{
			paneID:   pane.ID,
			paneName: pane.Meta.Name,
			proxy:    pane.IsProxy(),
		}, nil
	})
}

func (s *Session) queryPaneList() ([]paneListEntry, error) {
	return enqueueSessionQuery(s, func(s *Session) ([]paneListEntry, error) {
		entries := make([]paneListEntry, 0, len(s.Panes))
		w := s.activeWindow()
		now := s.clock().Now()
		for _, p := range s.Panes {
			idle := s.paneIdleStatus(p.ID, p.CreatedAt(), now)
			entry := paneListEntry{
				paneID:    p.ID,
				name:      p.Meta.Name,
				host:      p.Meta.Host,
				task:      p.Meta.Task,
				cwd:       effectivePaneCwd(p),
				gitBranch: p.Meta.GitBranch,
				idle:      idle.listDisplay(now, p.CreatedAt()),
				pr:        p.Meta.PR,
				prs:       proto.CloneTrackedPRs(p.Meta.TrackedPRs),
				issues:    proto.CloneTrackedIssues(p.Meta.TrackedIssues),
			}
			if w != nil && w.ActivePane != nil && w.ActivePane.ID == p.ID {
				entry.active = true
			}
			switch {
			case p.Meta.Dormant:
				entry.windowName = "(dormant)"
			default:
				if pw := s.findWindowByPaneID(p.ID); pw != nil {
					entry.windowName = pw.Name
					entry.lead = pw.LeadPaneID == p.ID
				}
			}
			entries = append(entries, entry)
		}
		return entries, nil
	})
}

func (s *Session) querySessionStatus() (sessionStatusSnapshot, error) {
	return enqueueSessionQuery(s, func(s *Session) (sessionStatusSnapshot, error) {
		snap := sessionStatusSnapshot{
			total:       len(s.Panes),
			windowCount: len(s.Windows),
		}
		if w := s.activeWindow(); w != nil && w.ZoomedPaneID != 0 {
			if pane := s.findPaneByID(w.ZoomedPaneID); pane != nil {
				snap.zoomed = pane.Meta.Name
			}
		}
		return snap, nil
	})
}

func (s *Session) queryWindowList() ([]windowListEntry, error) {
	return enqueueSessionQuery(s, func(s *Session) ([]windowListEntry, error) {
		entries := make([]windowListEntry, 0, len(s.Windows))
		for i, w := range s.Windows {
			entries = append(entries, windowListEntry{
				index:     i + 1,
				name:      w.Name,
				paneCount: w.PaneCount(),
				active:    w.ID == s.ActiveWindowID,
			})
		}
		return entries, nil
	})
}

func (s *Session) queryClientList() ([]clientListEntry, error) {
	return enqueueSessionQuery(s, func(s *Session) ([]clientListEntry, error) {
		clients := s.ensureClientManager().clients
		entries := make([]clientListEntry, 0, len(clients))
		sizeOwner := s.effectiveSizeClient()
		for _, cc := range clients {
			entries = append(entries, clientListEntry{
				id:           cc.ID,
				displayPanes: cc.displayPanesState(),
				chooser:      cc.chooserState(),
				size:         fmt.Sprintf("%dx%d", cc.cols, cc.rows),
				sizeOwner:    cc == sizeOwner,
				capabilities: cc.capabilitySummary(),
			})
		}
		return entries, nil
	})
}

func (s *Session) queryConnectionLog() ([]ConnectionLogEntry, error) {
	return enqueueSessionQuery(s, func(s *Session) ([]ConnectionLogEntry, error) {
		return s.ensureConnectionLog().Snapshot(), nil
	})
}

func (s *Session) queryPaneLog() ([]PaneLogEntry, error) {
	return enqueueSessionQuery(s, func(s *Session) ([]PaneLogEntry, error) {
		return s.ensurePaneLog().Snapshot(), nil
	})
}

func (s *Session) queryUIClient(requestedClientID, eventName string) (uiClientSnapshot, error) {
	return enqueueSessionQuery(s, func(s *Session) (uiClientSnapshot, error) {
		return s.resolveUIClientSnapshot(requestedClientID, eventName)
	})
}

func (s *Session) queryFirstClient() (*clientConn, error) {
	return enqueueSessionQuery(s, func(s *Session) (*clientConn, error) {
		if s.ensureClientManager().clientCount() == 0 {
			return nil, fmt.Errorf("no client attached")
		}
		return s.ensureClientManager().firstClient(), nil
	})
}
