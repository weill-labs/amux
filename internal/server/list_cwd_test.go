package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

func TestFormatListCwd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cwd  string
		home string
		max  int
		want string
	}{
		{
			name: "empty cwd",
			cwd:  "",
			home: "/Users/alice",
			max:  36,
			want: "",
		},
		{
			name: "non-positive width",
			cwd:  "/Users/alice/src/amux54",
			home: "/Users/alice",
			max:  0,
			want: "",
		},
		{
			name: "exact home",
			cwd:  "/Users/alice",
			home: "/Users/alice",
			max:  36,
			want: "~",
		},
		{
			name: "home descendant without truncation",
			cwd:  "/Users/alice/sync/github/amux/amux54",
			home: "/Users/alice",
			max:  36,
			want: "~/sync/github/amux/amux54",
		},
		{
			name: "long home descendant keeps tail",
			cwd:  "/Users/alice/src/clients/alpha/beta/gamma/delta/amux54",
			home: "/Users/alice",
			max:  20,
			want: "~/…/delta/amux54",
		},
		{
			name: "long non-home path keeps tail",
			cwd:  "/opt/worktrees/clients/alpha/beta/gamma/delta/amux54",
			home: "/Users/alice",
			max:  18,
			want: "…/delta/amux54",
		},
		{
			name: "tiny width truncates prefix",
			cwd:  "/Users/alice/src/amux54",
			home: "/Users/alice",
			max:  3,
			want: "~/…",
		},
		{
			name: "single long segment truncates tail",
			cwd:  "/Users/alice/superlongsegmentname",
			home: "/Users/alice",
			max:  8,
			want: "~/…/",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := formatListCwd(tt.cwd, tt.home, tt.max); got != tt.want {
				t.Fatalf("formatListCwd(%q, %q, %d) = %q, want %q", tt.cwd, tt.home, tt.max, got, tt.want)
			}
		})
	}
}

func TestFormatListCwdCollapsesResolvedHome(t *testing.T) {
	t.Parallel()

	realHome := t.TempDir()
	cwd := filepath.Join(realHome, "src", "amux54")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", cwd, err)
	}

	linkDir := t.TempDir()
	linkHome := filepath.Join(linkDir, "home")
	if err := os.Symlink(realHome, linkHome); err != nil {
		t.Fatalf("Symlink(%q, %q): %v", realHome, linkHome, err)
	}

	if got := formatListCwd(cwd, linkHome, 36); got != "~/src/amux54" {
		t.Fatalf("formatListCwd(%q, %q, 36) = %q, want %q", cwd, linkHome, got, "~/src/amux54")
	}
}

func TestFormatPaneListEmpty(t *testing.T) {
	t.Parallel()

	if got := formatPaneList(nil, "/Users/alice", true); got != "No panes.\n" {
		t.Fatalf("formatPaneList(nil) = %q, want %q", got, "No panes.\n")
	}
}

func TestFormatPaneListBranchIncludesPR(t *testing.T) {
	t.Parallel()

	entry := paneListEntry{gitBranch: "main", pr: "42"}
	if got := formatPaneListBranch(entry); got != "main #42" {
		t.Fatalf("formatPaneListBranch(%+v) = %q, want %q", entry, got, "main #42")
	}
}

func TestCmdListIncludesCwdAndSupportsNoCwd(t *testing.T) {
	t.Setenv("HOME", "/Users/alice")
	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	p1.ApplyCwdBranch("/Users/alice/src/clients/alpha/beta/gamma/delta/amux54", "main")
	p2 := newTestPane(sess, 2, "pane-2")
	p2.Meta.Dir = "/Users/alice/src/clients/worker"
	p2.Meta.Task = "build"
	w := newTestWindowWithPanes(t, sess, 1, "main", p1, p2)

	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1, p2}

	withCwd := runTestCommand(t, srv, sess, "list")
	if withCwd.cmdErr != "" {
		t.Fatalf("list error: %s", withCwd.cmdErr)
	}
	for _, want := range []string{"CWD", "~/…/alpha/beta/gamma/delta/amux54", "~/src/clients/worker", "build"} {
		if !strings.Contains(withCwd.output, want) {
			t.Fatalf("list output missing %q:\n%s", want, withCwd.output)
		}
	}

	noCwd := runTestCommand(t, srv, sess, "list", "--no-cwd")
	if noCwd.cmdErr != "" {
		t.Fatalf("list --no-cwd error: %s", noCwd.cmdErr)
	}
	if strings.Contains(noCwd.output, "CWD") {
		t.Fatalf("list --no-cwd should omit CWD header:\n%s", noCwd.output)
	}
	if strings.Contains(noCwd.output, "~/") {
		t.Fatalf("list --no-cwd should omit cwd values:\n%s", noCwd.output)
	}
}

func TestCmdListRejectsUnknownArgs(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "list", "--bogus")
	if res.cmdErr != "usage: list [--no-cwd]" {
		t.Fatalf("cmdErr = %q, want usage", res.cmdErr)
	}
}

func TestCmdListIncludesIdleColumnAndState(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	clk := NewFakeClock(time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC))
	mustSessionMutation(t, sess, func(sess *Session) {
		sess.Clock = clk
		sess.ensureIdleTracker().VTIdleSettle = 2 * time.Second
	})

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	w := newTestWindowWithPanes(t, sess, 1, "main", p1, p2)
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1, p2)

	mustSessionMutation(t, sess, func(sess *Session) {
		sess.ensureIdleTracker().TrackOutput(p1.ID, func() {}, func(time.Time) {})
	})
	clk.AwaitTimers(1)
	clk.Advance(3 * time.Second)

	mustSessionMutation(t, sess, func(sess *Session) {
		sess.ensureIdleTracker().TrackOutput(p2.ID, func() {}, func(time.Time) {})
	})
	clk.AwaitTimers(2)

	res := runTestCommand(t, srv, sess, "list")
	if res.cmdErr != "" {
		t.Fatalf("list error: %s", res.cmdErr)
	}
	if !strings.Contains(res.output, "IDLE") {
		t.Fatalf("list output missing IDLE header:\n%s", res.output)
	}

	var pane1Line, pane2Line string
	for _, line := range strings.Split(res.output, "\n") {
		switch {
		case strings.Contains(line, "pane-1"):
			pane1Line = line
		case strings.Contains(line, "pane-2"):
			pane2Line = line
		}
	}
	if !strings.Contains(pane1Line, "3s ago") {
		t.Fatalf("pane-1 should show settled idle age, got:\n%s", pane1Line)
	}
	if !strings.Contains(pane2Line, "--") {
		t.Fatalf("pane-2 should show -- while output is still active, got:\n%s", pane2Line)
	}
}

func TestIdleTrackerLastOutputWithoutSnapshot(t *testing.T) {
	t.Parallel()

	var tracker VTIdleTracker
	if got, ok := tracker.LastOutput(99); ok || !got.IsZero() {
		t.Fatalf("LastOutput() = (%v, %v), want (zero, false)", got, ok)
	}
}

func TestPaneIdleStatusUsesCreatedAtWhenNoOutput(t *testing.T) {
	t.Parallel()

	tracker := NewIdleTracker(testClockFn(RealClock{}))
	tracker.VTIdleSettle = 2 * time.Second
	createdAt := time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC)
	now := createdAt.Add(5 * time.Second)

	status := tracker.PaneStatus(1, createdAt, now)
	if !status.idle {
		t.Fatal("pane should be idle once createdAt+settle has passed without output")
	}
	if want := createdAt.Add(2 * time.Second); !status.idleSince.Equal(want) {
		t.Fatalf("idleSince = %v, want %v", status.idleSince, want)
	}
	if got := status.listDisplay(now, createdAt); got != "5s ago" {
		t.Fatalf("listDisplay() = %q, want %q", got, "5s ago")
	}
}

func TestPaneIdleStatusListDisplayClampsFutureBase(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC)
	status := paneIdleStatus{
		idle:          true,
		lastOutput:    now.Add(time.Second),
		hasLastOutput: true,
	}

	if got := status.listDisplay(now, now.Add(-time.Second)); got != "0s ago" {
		t.Fatalf("listDisplay() = %q, want %q", got, "0s ago")
	}
}

func TestFormatPaneListDefaultsEmptyIdleWithoutCwd(t *testing.T) {
	t.Parallel()

	out := formatPaneList([]paneListEntry{{
		paneID:     1,
		name:       "pane-1",
		host:       "local",
		windowName: "main",
		active:     true,
	}}, "/Users/alice", false)

	if !strings.Contains(out, "IDLE") {
		t.Fatalf("formatPaneList should include IDLE header:\n%s", out)
	}
	if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) < 2 || !strings.Contains(lines[1], "--") {
		t.Fatalf("formatPaneList should default IDLE to --:\n%s", out)
	}
}
