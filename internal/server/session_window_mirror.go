package server

import (
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

func mutationWindowByID(mctx *MutationContext, id uint32) *mux.Window {
	for _, w := range mctx.Windows {
		if w != nil && w.ID == id {
			return w
		}
	}
	return nil
}
