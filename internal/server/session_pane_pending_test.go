package server

import (
	"context"
	"testing"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func TestLocalPaneBuildResultPreservesPendingPaneMetadataUpdates(t *testing.T) {
	t.Parallel()

	sess := newSession("test-pending-pane-meta")
	stopCrashCheckpointLoop(t, sess)
	t.Cleanup(func() {
		stopSessionBackgroundLoops(t, sess)
		for _, pane := range sess.Panes {
			_ = pane.Close()
			_ = pane.WaitClosed()
		}
	})

	pending := newTestPane(sess, 1, "pane-1")
	window := newTestWindowWithPanes(t, sess, 1, "window-1", pending)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pending)

	mustSessionMutation(t, sess, func(sess *Session) {
		pane := sess.findPaneByID(pending.ID)
		pane.Meta.Task = "build"
		pane.Meta.GitBranch = "feat/pending-meta"
		pane.Meta.PR = "42"
		pane.Meta.KV = map[string]string{
			mux.PaneMetaKeyTask:   "build",
			mux.PaneMetaKeyBranch: "feat/pending-meta",
			mux.PaneMetaKeyPR:     "42",
			"issue":               "LAB-1923",
		}
		pane.Meta.TrackedPRs = []proto.TrackedPR{{Number: 42, Status: proto.TrackedStatusActive}}
		pane.SetMetaManualBranch(true)
	})

	built, err := mux.NewPaneWithScrollback(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 80, 23, "test", mux.DefaultScrollbackLines, sess.paneOutputCallback(), sess.paneExitCallback())
	if err != nil {
		t.Fatalf("NewPaneWithScrollback: %v", err)
	}
	mustSessionMutation(t, sess, func(sess *Session) {
		localPaneBuildResultEvent{
			placeholder: pending,
			pane:        built,
		}.handle(context.Background(), sess)
	})

	got := mustSessionQuery(t, sess, func(sess *Session) *mux.Pane {
		return sess.findPaneByID(pending.ID)
	})
	if got != built {
		t.Fatalf("replacement pane = %p, want built pane %p", got, built)
	}
	if got.Meta.Task != "build" || got.Meta.GitBranch != "feat/pending-meta" || got.Meta.PR != "42" {
		t.Fatalf("replacement metadata = %+v", got.Meta)
	}
	if got.Meta.KV["issue"] != "LAB-1923" {
		t.Fatalf("replacement metadata issue = %q, want LAB-1923", got.Meta.KV["issue"])
	}
	if len(got.Meta.TrackedPRs) != 1 || got.Meta.TrackedPRs[0].Number != 42 {
		t.Fatalf("replacement tracked PRs = %+v, want PR 42", got.Meta.TrackedPRs)
	}
	if !got.MetaManualBranch() {
		t.Fatal("replacement pane should preserve manual branch override")
	}
}
