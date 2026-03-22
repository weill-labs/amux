package server

import (
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
			want: "~/…/gamma/delta/amux54",
		},
		{
			name: "long non-home path keeps tail",
			cwd:  "/opt/worktrees/clients/alpha/beta/gamma/delta/amux54",
			home: "/Users/alice",
			max:  18,
			want: "…/delta/amux54",
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

func TestCmdListIncludesCwdAndSupportsNoCwd(t *testing.T) {
	t.Parallel()

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
	for _, want := range []string{"CWD", "~/…/gamma/delta/amux54", "~/src/clients/worker", "build"} {
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
