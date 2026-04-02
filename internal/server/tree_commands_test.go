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

func TestParseMoveSiblingArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		usage    string
		wantPane string
		wantErr  string
	}{
		{
			name:     "valid",
			args:     []string{"pane-1"},
			usage:    moveUpUsage,
			wantPane: "pane-1",
		},
		{
			name:    "missing pane",
			args:    nil,
			usage:   moveUpUsage,
			wantErr: moveUpUsage,
		},
		{
			name:    "too many args",
			args:    []string{"pane-1", "extra"},
			usage:   moveDownUsage,
			wantErr: moveDownUsage,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			paneRef, err := parseMoveSiblingArgs(tt.args, tt.usage)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseMoveSiblingArgs(%v, %q) error = %v, want %q", tt.args, tt.usage, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMoveSiblingArgs(%v, %q): %v", tt.args, tt.usage, err)
			}
			if paneRef != tt.wantPane {
				t.Fatalf("parseMoveSiblingArgs(%v, %q) = %q, want %q", tt.args, tt.usage, paneRef, tt.wantPane)
			}
		})
	}
}

func TestParseDropPaneArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantPane   string
		wantTarget string
		wantEdge   string
		wantErr    string
	}{
		{
			name:       "valid",
			args:       []string{"pane-1", "pane-2", "left"},
			wantPane:   "pane-1",
			wantTarget: "pane-2",
			wantEdge:   "left",
		},
		{
			name:    "too short",
			args:    []string{"pane-1", "pane-2"},
			wantErr: dropPaneUsage,
		},
		{
			name:    "invalid edge",
			args:    []string{"pane-1", "pane-2", "middle"},
			wantErr: dropPaneUsage,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			paneRef, targetRef, edge, err := parseDropPaneArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseDropPaneArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDropPaneArgs(%v): %v", tt.args, err)
			}
			if paneRef != tt.wantPane || targetRef != tt.wantTarget || edge != tt.wantEdge {
				t.Fatalf("parseDropPaneArgs(%v) = (%q, %q, %q), want (%q, %q, %q)", tt.args, paneRef, targetRef, edge, tt.wantPane, tt.wantTarget, tt.wantEdge)
			}
		})
	}
}

func TestDropPanePlacement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		edge      string
		wantDir   mux.SplitDir
		wantFirst bool
	}{
		{edge: "left", wantDir: mux.SplitVertical, wantFirst: true},
		{edge: "right", wantDir: mux.SplitVertical, wantFirst: false},
		{edge: "top", wantDir: mux.SplitHorizontal, wantFirst: true},
		{edge: "bottom", wantDir: mux.SplitHorizontal, wantFirst: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.edge, func(t *testing.T) {
			t.Parallel()

			gotDir, gotFirst := dropPanePlacement(tt.edge)
			if gotDir != tt.wantDir || gotFirst != tt.wantFirst {
				t.Fatalf("dropPanePlacement(%q) = (%v, %v), want (%v, %v)", tt.edge, gotDir, gotFirst, tt.wantDir, tt.wantFirst)
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

func TestQueuedCommandDropPaneErrorPaths(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	usageRes := runTestCommand(t, srv, sess, "drop-pane", "pane-1", "pane-2")
	if usageRes.cmdErr != dropPaneUsage {
		t.Fatalf("drop-pane usage error = %q", usageRes.cmdErr)
	}

	noSessionRes := runTestCommand(t, srv, sess, "drop-pane", "pane-1", "pane-2", "left")
	if noSessionRes.cmdErr != "no session" {
		t.Fatalf("drop-pane no session error = %q", noSessionRes.cmdErr)
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

	missingPane := runTestCommand(t, srv, sess, "drop-pane", "missing", "pane-2", "left")
	if missingPane.cmdErr != `pane "missing" not found` {
		t.Fatalf("drop-pane missing pane error = %q", missingPane.cmdErr)
	}

	missingTarget := runTestCommand(t, srv, sess, "drop-pane", "pane-1", "missing", "left")
	if missingTarget.cmdErr != `pane "missing" not found` {
		t.Fatalf("drop-pane missing target error = %q", missingTarget.cmdErr)
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

func TestQueuedCommandDropPaneSplitsTargetPaneAtEdge(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	p3 := newTestPane(sess, 3, "pane-3")
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "main"
	if _, err := w.SplitRoot(mux.SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot: %v", err)
	}
	w.FocusPane(p1)
	if _, err := w.SplitPaneWithOptions(p1.ID, mux.SplitHorizontal, p3, mux.SplitOptions{}); err != nil {
		t.Fatalf("SplitPaneWithOptions: %v", err)
	}
	if err := setAttachTestLayout(sess, []*mux.Window{w}, w.ID, []*mux.Pane{p1, p2, p3}); err != nil {
		t.Fatalf("setAttachTestLayout: %v", err)
	}

	res := runTestCommand(t, srv, sess, "drop-pane", "pane-1", "pane-2", "left")
	if res.cmdErr != "" {
		t.Fatalf("drop-pane edge split error = %q", res.cmdErr)
	}

	c1 := w.Root.FindPane(p1.ID)
	c2 := w.Root.FindPane(p2.ID)
	c3 := w.Root.FindPane(p3.ID)
	if c1 == nil || c2 == nil || c3 == nil {
		t.Fatalf("expected all panes after drop-pane, got c1=%v c2=%v c3=%v", c1, c2, c3)
	}
	if !(c3.X < c1.X && c1.X < c2.X && c1.Y == c2.Y) {
		t.Fatalf("expected pane-1 to split pane-2 on the left: c1=(%d,%d %dx%d) c2=(%d,%d %dx%d) c3=(%d,%d %dx%d)",
			c1.X, c1.Y, c1.W, c1.H,
			c2.X, c2.Y, c2.W, c2.H,
			c3.X, c3.Y, c3.W, c3.H,
		)
	}
}

func TestQueuedCommandDropPaneCreatesRootSplitAtWindowEdge(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

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

	res := runTestCommand(t, srv, sess, "drop-pane", "pane-2", "root", "left")
	if res.cmdErr != "" {
		t.Fatalf("drop-pane root split error = %q", res.cmdErr)
	}

	c1 := w.Root.FindPane(p1.ID)
	c2 := w.Root.FindPane(p2.ID)
	if c1 == nil || c2 == nil {
		t.Fatalf("expected both panes after root drop, got c1=%v c2=%v", c1, c2)
	}
	if c2.X >= c1.X {
		t.Fatalf("expected pane-2 to become the left root pane: c1=(%d,%d %dx%d) c2=(%d,%d %dx%d)",
			c1.X, c1.Y, c1.W, c1.H,
			c2.X, c2.Y, c2.W, c2.H,
		)
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
	if order[0] != p2.ID || order[1] != p3.ID || order[2] != p1.ID {
		t.Fatalf("move-up/down order = %v, want [%d %d %d]", order, p2.ID, p3.ID, p1.ID)
	}
}

func TestQueuedCommandMoveUpDownErrorPaths(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	moveUpUsageRes := runTestCommand(t, srv, sess, "move-up")
	if moveUpUsageRes.cmdErr != moveUpUsage {
		t.Fatalf("move-up usage error = %q", moveUpUsageRes.cmdErr)
	}

	moveDownNoSession := runTestCommand(t, srv, sess, "move-down", "pane-1")
	if moveDownNoSession.cmdErr != "no session" {
		t.Fatalf("move-down no session error = %q", moveDownNoSession.cmdErr)
	}

	p1 := newTestPane(sess, 1, "pane-1")
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "main"
	w.FocusPane(p1)
	p2 := newTestPane(sess, 2, "pane-2")
	if _, err := w.Split(mux.SplitHorizontal, p2); err != nil {
		t.Fatalf("Split horizontal: %v", err)
	}
	if err := setAttachTestLayout(sess, []*mux.Window{w}, w.ID, []*mux.Pane{p1, p2}); err != nil {
		t.Fatalf("setAttachTestLayout: %v", err)
	}

	if got := runTestCommand(t, srv, sess, "move-up", "pane-1").cmdErr; !strings.Contains(got, "already first in its split group") {
		t.Fatalf("move-up first pane error = %q", got)
	}
	if got := runTestCommand(t, srv, sess, "move-down", "pane-2").cmdErr; !strings.Contains(got, "already last in its split group") {
		t.Fatalf("move-down last pane error = %q", got)
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
