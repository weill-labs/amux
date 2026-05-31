package server

import (
	"context"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/remote"
	mirrorpkg "github.com/weill-labs/amux/internal/server/mirror"
)

// windowReconcileInput carries the parsed remote window snapshot from the mirror
// goroutine into the session event loop, where the reconcile runs.
type windowReconcileInput struct {
	localWindowID uint32
	ref           mirrorpkg.WindowRef
	ws            proto.WindowSnapshot
	leaves        []remoteWindowLeaf
	signature     string
}

// enqueueWindowLayoutReconcile is the mirror.Manager OnWindowLayout callback. It
// runs on the mirror goroutine: it parses the remote layout, then enqueues a
// mutation that reconciles the local mirror window. Non-structural broadcasts
// (idle/busy, metadata) are filtered out by the structural signature inside the
// mutation, so this stays cheap on the hot path.
func (s *Session) enqueueWindowLayoutReconcile(localWindowID uint32, ref mirrorpkg.WindowRef, layout *proto.LayoutSnapshot) {
	if s == nil || s.shutdown.Load() || layout == nil {
		return
	}
	ws, err := remote.ResolveWindowFromLayout(layout, ref.WindowName)
	if err != nil {
		return
	}
	leaves, err := planRemoteWindowLeaves(ws)
	if err != nil {
		return
	}
	in := windowReconcileInput{
		localWindowID: localWindowID,
		ref:           ref,
		ws:            ws,
		leaves:        leaves,
		signature:     remoteWindowSignature(ws),
	}
	s.enqueueCommandMutationContext(s.context(), func(mctx *MutationContext) commandMutationResult {
		return mctx.applyWindowReconcile(in)
	})
}

// applyWindowReconcile diffs the remote window structure against the local
// mirror window and brings the local window into line: it adds proxy panes for
// new remote panes, removes proxies whose remote pane is gone, and rebuilds the
// split tree to match. Panes are matched across the two sides by remote name.
func (mctx *MutationContext) applyWindowReconcile(in windowReconcileInput) commandMutationResult {
	sess := mctx.sess
	if sess == nil {
		return commandMutationResult{}
	}
	if sess.windowMirrorSigs == nil {
		sess.windowMirrorSigs = make(map[uint32]string)
	}
	if sess.windowMirrorSigs[in.localWindowID] == in.signature {
		return commandMutationResult{} // structure unchanged — nothing to do
	}

	w := mutationWindowByID(mctx, in.localWindowID)
	if w == nil {
		// Local mirror window is gone: stop the subscription and forget it.
		delete(sess.windowMirrorSigs, in.localWindowID)
		sess.mirror.DetachWindow(in.localWindowID)
		return commandMutationResult{}
	}

	existing := existingMirrorPanesByRemoteName(sess, w)

	paneMap := make(map[uint32]*mux.Pane, len(in.leaves))
	created := make([]*mux.Pane, 0)
	createdRefs := make(map[uint32]checkpoint.RemoteRef)
	keep := make(map[string]bool, len(in.leaves))
	for _, leaf := range in.leaves {
		keep[leaf.name] = true
		if p, ok := existing[leaf.name]; ok {
			paneMap[leaf.remoteID] = p
			continue
		}
		ref := checkpoint.RemoteRef{Host: in.ref.Host, Session: in.ref.Session, PaneName: leaf.name}
		pane, err := mctx.prepareMirrorPane(mux.PaneMeta{}, ref, leaf.cols, leaf.rows)
		if err != nil {
			for _, cp := range created {
				mctx.removePane(cp.ID)
				mctx.ScheduleClose(cp)
			}
			return commandMutationResult{err: err}
		}
		paneMap[leaf.remoteID] = pane
		created = append(created, pane)
		createdRefs[leaf.remoteID] = ref
	}

	for remoteID, ref := range createdRefs {
		_ = mctx.trackMirrorPane(paneMap[remoteID], ref)
	}

	// Rebuild the local window's split tree to match the remote structure,
	// keeping the local window identity and refitting to local dimensions.
	rebuilt := mux.RebuildWindowFromSnapshot(in.ws, w.Width, w.Height, paneMap)
	w.Root = rebuilt.Root
	w.LeadPaneID = 0
	w.ZoomedPaneID = 0
	// RebuildWindowFromSnapshot already falls back to the first leaf when the
	// active pane id is not mapped, so this is non-nil for any non-empty tree.
	if rebuilt.ActivePane != nil {
		w.ActivePane = rebuilt.ActivePane
	}
	w.Resize(w.Width, w.Height)

	// Close proxies whose remote counterpart disappeared.
	for name, pane := range existing {
		if !keep[name] {
			sess.mirror.Detach(pane.ID)
			mctx.removePane(pane.ID)
			mctx.ScheduleClose(pane)
		}
	}

	sess.windowMirrorSigs[in.localWindowID] = in.signature
	return commandMutationResult{broadcastLayout: true}
}

// existingMirrorPanesByRemoteName maps each of a window's proxy panes to the
// remote pane name it mirrors, so reconcile can match panes across resyncs.
func existingMirrorPanesByRemoteName(sess *Session, w *mux.Window) map[string]*mux.Pane {
	out := make(map[string]*mux.Pane)
	if w == nil || w.Root == nil {
		return out
	}
	w.Root.Walk(func(c *mux.LayoutCell) {
		if c.Pane == nil {
			return
		}
		if ref, ok := sess.mirror.RemoteRef(c.Pane.ID); ok {
			out[ref.PaneName] = c.Pane
		}
	})
	return out
}

// resizeMirrorTargetWindow applies a mirror subscriber's requested size to a
// window. Called on the session event loop from the attach-window handler.
func (s *Session) resizeMirrorTargetWindow(w *mux.Window, cols, rows int) {
	if w == nil || cols <= 0 || rows <= 0 || (w.Width == cols && w.Height == rows) {
		return
	}
	w.Resize(cols, rows)
}

// windowMirrorCheckpoints builds checkpoint records for the session's active
// window mirrors so their layout subscriptions can resume after a reload.
func windowMirrorCheckpoints(s *Session) map[uint32]checkpoint.RemoteWindowRef {
	if s == nil || s.mirror == nil || len(s.windowMirrorSigs) == 0 {
		return nil
	}
	infos := s.mirror.WindowMirrorInfos()
	out := make(map[uint32]checkpoint.RemoteWindowRef, len(s.windowMirrorSigs))
	for id, sig := range s.windowMirrorSigs {
		info, ok := infos[id]
		if !ok {
			continue
		}
		out[id] = checkpoint.RemoteWindowRef{
			Host:       info.Ref.Host,
			Session:    info.Ref.Session,
			WindowName: info.Ref.WindowName,
			Cols:       info.Cols,
			Rows:       info.Rows,
			Signature:  sig,
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// restoreWindowMirrors re-establishes window layout subscriptions after a reload
// and re-seeds their structural signatures so the first broadcast is a no-op.
func (s *Session) restoreWindowMirrors(refs map[uint32]checkpoint.RemoteWindowRef) {
	if s == nil || s.mirror == nil || len(refs) == 0 {
		return
	}
	if s.windowMirrorSigs == nil {
		s.windowMirrorSigs = make(map[uint32]string, len(refs))
	}
	for id, ref := range refs {
		s.windowMirrorSigs[id] = ref.Signature
		_ = s.mirror.TrackWindow(id, mirrorpkg.WindowRef{
			Host:       ref.Host,
			Session:    ref.Session,
			WindowName: ref.WindowName,
		}, ref.Cols, ref.Rows)
	}
}

// pushMirrorWindowSize forwards a local mirror window's size to the remote so it
// re-renders the window to match. A no-op for non-mirror windows. Event-loop only.
func (s *Session) pushMirrorWindowSize(w *mux.Window) {
	if s == nil || w == nil || s.mirror == nil {
		return
	}
	if _, ok := s.windowMirrorSigs[w.ID]; !ok {
		return // not a mirrored window
	}
	s.mirror.ResizeWindow(w.ID, w.Width, w.Height)
}

// enqueueResizeMirrorWindow handles a resize a window-mirror subscriber sent over
// its layout link, so the remote re-renders the window at the local size.
func (s *Session) enqueueResizeMirrorWindow(windowID uint32, cols, rows int) {
	if s == nil || s.shutdown.Load() || windowID == 0 || cols <= 0 || rows <= 0 {
		return
	}
	s.enqueueEvent(s.context(), resizeMirrorWindowEvent{windowID: windowID, cols: cols, rows: rows})
}

type resizeMirrorWindowEvent struct {
	windowID uint32
	cols     int
	rows     int
}

func (e resizeMirrorWindowEvent) handle(_ context.Context, s *Session) {
	w := s.windowByID(e.windowID)
	if w == nil || (w.Width == e.cols && w.Height == e.rows) {
		return
	}
	w.Resize(e.cols, e.rows)
	s.broadcastLayoutNow()
}

func mutationWindowByID(mctx *MutationContext, id uint32) *mux.Window {
	for _, w := range mctx.Windows {
		if w != nil && w.ID == id {
			return w
		}
	}
	return nil
}
