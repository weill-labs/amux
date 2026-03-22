package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
