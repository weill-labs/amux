package server

import (
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

func TestParseMoveArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantPane   string
		wantTarget string
		wantBefore bool
		wantErr    string
	}{
		{
			name:       "before",
			args:       []string{"pane-1", "--before", "pane-2"},
			wantPane:   "pane-1",
			wantTarget: "pane-2",
			wantBefore: true,
		},
		{
			name:       "after",
			args:       []string{"pane-1", "--after", "pane-2"},
			wantPane:   "pane-1",
			wantTarget: "pane-2",
			wantBefore: false,
		},
		{
			name:    "too short",
			args:    []string{"pane-1", "--before"},
			wantErr: moveUsage,
		},
		{
			name:    "too many args",
			args:    []string{"pane-1", "--before", "pane-2", "--after", "pane-3"},
			wantErr: moveUsage,
		},
		{
			name:    "invalid flag",
			args:    []string{"pane-1", "--around", "pane-2"},
			wantErr: moveUsage,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			paneRef, targetRef, before, err := parseMoveArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseMoveArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMoveArgs(%v): %v", tt.args, err)
			}
			if paneRef != tt.wantPane || targetRef != tt.wantTarget || before != tt.wantBefore {
				t.Fatalf("parseMoveArgs(%v) = (%q, %q, %v), want (%q, %q, %v)", tt.args, paneRef, targetRef, before, tt.wantPane, tt.wantTarget, tt.wantBefore)
			}
		})
	}
}

func TestParseMoveToArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantPane   string
		wantTarget string
		wantErr    string
	}{
		{
			name:       "valid",
			args:       []string{"pane-1", "pane-2"},
			wantPane:   "pane-1",
			wantTarget: "pane-2",
		},
		{
			name:    "too short",
			args:    []string{"pane-1"},
			wantErr: moveToUsage,
		},
		{
			name:    "too many args",
			args:    []string{"pane-1", "pane-2", "pane-3"},
			wantErr: moveToUsage,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			paneRef, targetRef, err := parseMoveToArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseMoveToArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMoveToArgs(%v): %v", tt.args, err)
			}
			if paneRef != tt.wantPane || targetRef != tt.wantTarget {
				t.Fatalf("parseMoveToArgs(%v) = (%q, %q), want (%q, %q)", tt.args, paneRef, targetRef, tt.wantPane, tt.wantTarget)
			}
		})
	}
}

func TestQueuedCommandSwapTreeErrorPaths(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	usageRes := runTestCommand(t, srv, sess, "swap-tree", "pane-1")
	if usageRes.cmdErr != "usage: swap-tree <pane1> <pane2>" {
		t.Fatalf("swap-tree usage error = %q", usageRes.cmdErr)
	}

	noSessionRes := runTestCommand(t, srv, sess, "swap-tree", "pane-1", "pane-2")
	if noSessionRes.cmdErr != "no session" {
		t.Fatalf("swap-tree no session error = %q", noSessionRes.cmdErr)
	}

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "main"
	if _, err := w.SplitRoot(mux.SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot: %v", err)
	}
	if err := setAttachTestLayout(sess, []*mux.Window{w}, w.ID, []*mux.Pane{p1, p2}); err != nil {
		t.Fatalf("setAttachTestLayout: %v", err)
	}

	missingFirst := runTestCommand(t, srv, sess, "swap-tree", "missing", "pane-1")
	if missingFirst.cmdErr != `pane "missing" not found` {
		t.Fatalf("swap-tree missing first pane error = %q", missingFirst.cmdErr)
	}

	missingSecond := runTestCommand(t, srv, sess, "swap-tree", "pane-1", "missing")
	if missingSecond.cmdErr != `pane "missing" not found` {
		t.Fatalf("swap-tree missing second pane error = %q", missingSecond.cmdErr)
	}

	p3 := newTestPane(sess, 3, "pane-3")
	if err := splitQueuedCommandTestWindow(sess, p1, p3); err != nil {
		t.Fatalf("splitQueuedCommandTestWindow: %v", err)
	}

	sameGroup := runTestCommand(t, srv, sess, "swap-tree", "pane-1", "pane-3")
	if !strings.Contains(sameGroup.cmdErr, "same root-level group") {
		t.Fatalf("swap-tree same group error = %q", sameGroup.cmdErr)
	}
}

func TestQueuedCommandMoveErrorPaths(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	usageRes := runTestCommand(t, srv, sess, "move", "pane-1", "--before")
	if usageRes.cmdErr != moveUsage {
		t.Fatalf("move usage error = %q", usageRes.cmdErr)
	}

	noSessionRes := runTestCommand(t, srv, sess, "move", "pane-1", "--before", "pane-2")
	if noSessionRes.cmdErr != "no session" {
		t.Fatalf("move no session error = %q", noSessionRes.cmdErr)
	}

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "main"
	if _, err := w.SplitRoot(mux.SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot: %v", err)
	}
	if err := setAttachTestLayout(sess, []*mux.Window{w}, w.ID, []*mux.Pane{p1, p2}); err != nil {
		t.Fatalf("setAttachTestLayout: %v", err)
	}

	missingPane := runTestCommand(t, srv, sess, "move", "missing", "--before", "pane-1")
	if missingPane.cmdErr != `pane "missing" not found` {
		t.Fatalf("move missing pane error = %q", missingPane.cmdErr)
	}

	missingTarget := runTestCommand(t, srv, sess, "move", "pane-1", "--before", "missing")
	if missingTarget.cmdErr != `pane "missing" not found` {
		t.Fatalf("move missing target error = %q", missingTarget.cmdErr)
	}

	p3 := newTestPane(sess, 3, "pane-3")
	if err := splitQueuedCommandTestWindow(sess, p1, p3); err != nil {
		t.Fatalf("splitQueuedCommandTestWindow: %v", err)
	}

	sameGroup := runTestCommand(t, srv, sess, "move", "pane-3", "--before", "pane-1")
	if sameGroup.cmdErr != "" {
		t.Fatalf("move same split group error = %q", sameGroup.cmdErr)
	}
	order := mustSessionQuery(t, sess, func(sess *Session) []uint32 {
		return []uint32{
			sess.Windows[0].Root.Children[0].Children[0].Pane.ID,
			sess.Windows[0].Root.Children[0].Children[1].Pane.ID,
		}
	})
	if order[0] != p3.ID || order[1] != p1.ID {
		t.Fatalf("same-group order = %v, want [%d %d]", order, p3.ID, p1.ID)
	}
}

func TestQueuedCommandMoveToErrorPaths(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	usageRes := runTestCommand(t, srv, sess, "move-to", "pane-1")
	if usageRes.cmdErr != moveToUsage {
		t.Fatalf("move-to usage error = %q", usageRes.cmdErr)
	}

	noSessionRes := runTestCommand(t, srv, sess, "move-to", "pane-1", "pane-2")
	if noSessionRes.cmdErr != "no session" {
		t.Fatalf("move-to no session error = %q", noSessionRes.cmdErr)
	}

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "main"
	if _, err := w.SplitRoot(mux.SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot: %v", err)
	}
	if err := setAttachTestLayout(sess, []*mux.Window{w}, w.ID, []*mux.Pane{p1, p2}); err != nil {
		t.Fatalf("setAttachTestLayout: %v", err)
	}

	missingPane := runTestCommand(t, srv, sess, "move-to", "missing", "pane-1")
	if missingPane.cmdErr != `pane "missing" not found` {
		t.Fatalf("move-to missing pane error = %q", missingPane.cmdErr)
	}

	missingTarget := runTestCommand(t, srv, sess, "move-to", "pane-1", "missing")
	if missingTarget.cmdErr != `pane "missing" not found` {
		t.Fatalf("move-to missing target error = %q", missingTarget.cmdErr)
	}
}

func TestQueuedCommandMoveUpDown(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "main"
	w.FocusPane(p1)
	p2 := newTestPane(sess, 2, "pane-2")
	if _, err := w.Split(mux.SplitHorizontal, p2); err != nil {
		t.Fatalf("Split horizontal: %v", err)
	}
	p3 := newTestPane(sess, 3, "pane-3")
	if _, err := w.Split(mux.SplitHorizontal, p3); err != nil {
		t.Fatalf("Split horizontal again: %v", err)
	}
	if err := setAttachTestLayout(sess, []*mux.Window{w}, w.ID, []*mux.Pane{p1, p2, p3}); err != nil {
		t.Fatalf("setAttachTestLayout: %v", err)
	}

	moveDown := runTestCommand(t, srv, sess, "move-down", "pane-1")
	if moveDown.cmdErr != "" {
		t.Fatalf("move-down error = %q", moveDown.cmdErr)
	}
	moveUp := runTestCommand(t, srv, sess, "move-up", "pane-3")
	if moveUp.cmdErr != "" {
		t.Fatalf("move-up error = %q", moveUp.cmdErr)
	}
	order := mustSessionQuery(t, sess, func(sess *Session) []uint32 {
		return []uint32{
			sess.Windows[0].Root.Children[0].Pane.ID,
			sess.Windows[0].Root.Children[1].Pane.ID,
			sess.Windows[0].Root.Children[2].Pane.ID,
		}
	})
	if order[0] != p3.ID || order[1] != p2.ID || order[2] != p1.ID {
		t.Fatalf("move-up/down order = %v, want [%d %d %d]", order, p3.ID, p2.ID, p1.ID)
	}
}

func splitQueuedCommandTestWindow(sess *Session, activePane, newPane *mux.Pane) error {
	_, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
		w := sess.activeWindow()
		w.FocusPane(activePane)
		if _, err := w.Split(mux.SplitHorizontal, newPane); err != nil {
			return struct{}{}, err
		}
		sess.Panes = append(sess.Panes, newPane)
		return struct{}{}, nil
	})
	return err
}
