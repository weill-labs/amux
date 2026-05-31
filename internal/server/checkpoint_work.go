package server

import (
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/mailbox"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

type paneHistoryScreenSnapshotFunc func(*mux.Pane) ([]string, string, uint64)

type reloadCheckpointWork struct {
	cp      *checkpoint.ServerCheckpoint
	panes   []checkpointPaneWork
	mailbox mailbox.SnapshotSource
}

type crashCheckpointWork struct {
	cp      *checkpoint.CrashCheckpoint
	panes   []checkpointPaneWork
	mailbox mailbox.SnapshotSource
}

type checkpointPaneWork struct {
	pane         *mux.Pane
	id           uint32
	meta         mux.PaneMeta
	manualBranch bool
	cols         int
	rows         int
	createdAt    time.Time
	isProxy      bool
	remoteRef    *checkpoint.RemoteRef
	ptmxFd       int
	pid          int
}

func (w checkpointPaneWork) paneCheckpoint(snapshot paneHistoryScreenSnapshot) checkpoint.PaneCheckpoint {
	return checkpoint.PaneCheckpoint{
		ID:           w.id,
		Meta:         w.meta,
		ManualBranch: w.manualBranch,
		PtmxFd:       w.ptmxFd,
		PID:          w.pid,
		Cols:         w.cols,
		Rows:         w.rows,
		History:      snapshot.history,
		Screen:       snapshot.screen,
		CreatedAt:    w.createdAt,
		IsProxy:      w.isProxy,
		RemoteRef:    w.remoteRef,
	}
}

func (w checkpointPaneWork) crashPaneState(snapshot paneHistoryScreenSnapshot) checkpoint.CrashPaneState {
	return checkpoint.CrashPaneState{
		ID:           w.id,
		Meta:         w.meta,
		ManualBranch: w.manualBranch,
		Cols:         w.cols,
		Rows:         w.rows,
		History:      snapshot.history,
		Screen:       snapshot.screen,
		CreatedAt:    w.createdAt,
		IsProxy:      w.isProxy,
		RemoteRef:    w.remoteRef,
	}
}

func (s *Session) collectCheckpointPaneWork() []checkpointPaneWork {
	work := make([]checkpointPaneWork, len(s.Panes))
	for i, p := range s.Panes {
		cols, rows := s.checkpointPaneDimensions(p.ID)
		item := checkpointPaneWork{
			pane:         p,
			id:           p.ID,
			meta:         cloneCheckpointPaneMeta(p.Meta),
			manualBranch: p.MetaManualBranch(),
			cols:         cols,
			rows:         rows,
			createdAt:    p.CreatedAt(),
			isProxy:      p.IsProxy(),
		}
		if item.isProxy {
			item.ptmxFd = -1
			if s.mirror != nil {
				if ref, ok := s.mirror.RemoteRef(p.ID); ok {
					item.remoteRef = ref
				}
			}
		} else {
			item.ptmxFd = p.PtmxFd()
			item.pid = p.ProcessPid()
		}
		work[i] = item
	}
	return work
}

func (s *Session) checkpointPaneDimensions(paneID uint32) (int, int) {
	for _, w := range s.Windows {
		if cell := w.Root.FindPane(paneID); cell != nil {
			return cell.W, mux.PaneContentHeight(cell.H)
		}
	}
	return 0, 0
}

func checkpointPaneRefs(work []checkpointPaneWork) []*mux.Pane {
	panes := make([]*mux.Pane, len(work))
	for i := range work {
		panes[i] = work[i].pane
	}
	return panes
}

func cloneCheckpointPaneMeta(meta mux.PaneMeta) mux.PaneMeta {
	meta.KV = mux.CloneMetaKV(meta.KV)
	meta.TrackedPRs = proto.CloneTrackedPRs(meta.TrackedPRs)
	meta.TrackedIssues = proto.CloneTrackedIssues(meta.TrackedIssues)
	return meta
}

func mailboxCheckpointSnapshotSource(store *mailbox.Store) mailbox.SnapshotSource {
	if store == nil || store.Len() == 0 {
		return mailbox.SnapshotSource{}
	}
	return store.SnapshotSource()
}

func materializeMailboxCheckpointSnapshot(source mailbox.SnapshotSource) *mailbox.Snapshot {
	if source.Empty() {
		return nil
	}
	snapshot := source.Snapshot()
	return &snapshot
}
