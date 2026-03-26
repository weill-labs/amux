package server

import (
	"errors"
	"testing"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

type stubTrackedMetaResolver struct {
	prStatus    map[int]proto.TrackedStatus
	prErr       map[int]error
	issueStatus map[string]proto.TrackedStatus
	issueErr    map[string]error
	prCalls     []stubPRCall
	issueCalls  []string
}

type stubPRCall struct {
	cwd    string
	number int
}

func (r *stubTrackedMetaResolver) ResolvePR(cwd string, number int) (proto.TrackedStatus, error) {
	r.prCalls = append(r.prCalls, stubPRCall{cwd: cwd, number: number})
	if err := r.prErr[number]; err != nil {
		return proto.TrackedStatusUnknown, err
	}
	if status, ok := r.prStatus[number]; ok {
		return status, nil
	}
	return proto.TrackedStatusActive, nil
}

func (r *stubTrackedMetaResolver) ResolveIssue(id string) (proto.TrackedStatus, error) {
	r.issueCalls = append(r.issueCalls, id)
	if err := r.issueErr[id]; err != nil {
		return proto.TrackedStatusUnknown, err
	}
	if status, ok := r.issueStatus[id]; ok {
		return status, nil
	}
	return proto.TrackedStatusActive, nil
}

func TestCmdRefreshMetaUsesActivePaneWhenPaneOmitted(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	resolver := &stubTrackedMetaResolver{
		prStatus: map[int]proto.TrackedStatus{
			42: proto.TrackedStatusCompleted,
		},
		issueStatus: map[string]proto.TrackedStatus{
			"LAB-450": proto.TrackedStatusCompleted,
		},
	}
	sess.TrackedMetaResolver = resolver

	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(0),
		Dir:   "/tmp/repo",
		TrackedPRs: []proto.TrackedPR{
			{Number: 42, Status: proto.TrackedStatusUnknown},
		},
		TrackedIssues: []proto.TrackedIssue{
			{ID: "LAB-450", Status: proto.TrackedStatusUnknown},
		},
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	sess.Windows = []*mux.Window{window}
	sess.ActiveWindowID = window.ID
	sess.Panes = []*mux.Pane{pane}

	res := runTestCommand(t, srv, sess, "refresh-meta")
	if res.cmdErr != "" {
		t.Fatalf("refresh-meta error: %s", res.cmdErr)
	}

	meta := mustSessionQuery(t, sess, func(sess *Session) mux.PaneMeta {
		return sess.findPaneByID(pane.ID).Meta
	})
	if got := meta.TrackedPRs[0].Status; got != proto.TrackedStatusCompleted {
		t.Fatalf("tracked PR status = %q, want completed", got)
	}
	if meta.TrackedPRs[0].Stale {
		t.Fatal("tracked PR should not be stale after successful refresh")
	}
	if meta.TrackedPRs[0].CheckedAt == "" {
		t.Fatal("tracked PR checked_at should be set")
	}
	if got := meta.TrackedIssues[0].Status; got != proto.TrackedStatusCompleted {
		t.Fatalf("tracked issue status = %q, want completed", got)
	}
	if meta.TrackedIssues[0].Stale {
		t.Fatal("tracked issue should not be stale after successful refresh")
	}
	if meta.TrackedIssues[0].CheckedAt == "" {
		t.Fatal("tracked issue checked_at should be set")
	}

	if len(resolver.prCalls) != 1 || resolver.prCalls[0].cwd != "/tmp/repo" || resolver.prCalls[0].number != 42 {
		t.Fatalf("resolver PR calls = %#v, want [/tmp/repo 42]", resolver.prCalls)
	}
	if len(resolver.issueCalls) != 1 || resolver.issueCalls[0] != "LAB-450" {
		t.Fatalf("resolver issue calls = %#v, want [LAB-450]", resolver.issueCalls)
	}
}

func TestCmdAddMetaReaddsRefreshExistingRefsAndMarksFailuresStale(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	resolver := &stubTrackedMetaResolver{
		prErr: map[int]error{
			42: errors.New("gh failed"),
		},
		issueErr: map[string]error{
			"LAB-450": errors.New("linear failed"),
		},
	}
	sess.TrackedMetaResolver = resolver

	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(0),
		Dir:   "/tmp/repo",
		TrackedPRs: []proto.TrackedPR{
			{Number: 42, Status: proto.TrackedStatusCompleted, CheckedAt: "old-pr"},
		},
		TrackedIssues: []proto.TrackedIssue{
			{ID: "LAB-450", Status: proto.TrackedStatusActive, CheckedAt: "old-issue"},
		},
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	sess.Windows = []*mux.Window{window}
	sess.ActiveWindowID = window.ID
	sess.Panes = []*mux.Pane{pane}

	res := runTestCommand(t, srv, sess, "add-meta", "pane-1", "pr=42", "issue=LAB-450")
	if res.cmdErr != "" {
		t.Fatalf("add-meta error: %s", res.cmdErr)
	}

	meta := mustSessionQuery(t, sess, func(sess *Session) mux.PaneMeta {
		return sess.findPaneByID(pane.ID).Meta
	})
	if got := meta.TrackedPRs[0].Status; got != proto.TrackedStatusCompleted {
		t.Fatalf("tracked PR status after failed refresh = %q, want completed", got)
	}
	if !meta.TrackedPRs[0].Stale {
		t.Fatal("tracked PR should be stale after failed refresh")
	}
	if meta.TrackedPRs[0].CheckedAt == "" || meta.TrackedPRs[0].CheckedAt == "old-pr" {
		t.Fatalf("tracked PR checked_at = %q, want updated timestamp", meta.TrackedPRs[0].CheckedAt)
	}
	if got := meta.TrackedIssues[0].Status; got != proto.TrackedStatusActive {
		t.Fatalf("tracked issue status after failed refresh = %q, want active", got)
	}
	if !meta.TrackedIssues[0].Stale {
		t.Fatal("tracked issue should be stale after failed refresh")
	}
	if meta.TrackedIssues[0].CheckedAt == "" || meta.TrackedIssues[0].CheckedAt == "old-issue" {
		t.Fatalf("tracked issue checked_at = %q, want updated timestamp", meta.TrackedIssues[0].CheckedAt)
	}
}

func TestCmdRefreshMetaUsage(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "refresh-meta", "pane-1", "extra")
	if got := res.cmdErr; got != "usage: refresh-meta [pane]" {
		t.Fatalf("refresh-meta usage error = %q, want usage string", got)
	}
}
