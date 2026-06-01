package server

import (
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/mux"
	mirrorpkg "github.com/weill-labs/amux/internal/server/mirror"
)

const remoteMirrorCreatePaneTestHost = "lab-2009-missing-host"

func TestCommandSpawnAtMirroredWindowFailsWhenRemoteHostMissing(t *testing.T) {
	t.Setenv("AMUX_CONFIG", t.TempDir()+"/config.toml")

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := seedMirroredCreatePaneWindow(t, sess, true, true)

	res := runTestCommand(t, srv, sess, "spawn", "--at", pane.Meta.Name, "--name", "worker")
	if !strings.Contains(res.cmdErr, `remote "lab-2009-missing-host" not found`) {
		t.Fatalf("spawn error = %q, want missing remote host", res.cmdErr)
	}
	assertPaneCount(t, sess, 1)
}

func TestCommandSpawnWindowMirroredWindowFailsWhenWindowMirrorUntracked(t *testing.T) {
	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	seedMirroredCreatePaneWindow(t, sess, false, false)

	res := runTestCommand(t, srv, sess, "spawn", "--window", "mirror-window", "--name", "worker")
	if !strings.Contains(res.cmdErr, `window "mirror-window" is not tracked by the remote mirror manager`) {
		t.Fatalf("spawn --window error = %q, want untracked mirror window", res.cmdErr)
	}
	assertPaneCount(t, sess, 1)
}

func TestCommandSplitMirroredPaneFailsWhenPaneMirrorUntracked(t *testing.T) {
	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := seedMirroredCreatePaneWindow(t, sess, true, false)

	res := runTestCommand(t, srv, sess, "split", pane.Meta.Name, "--name", "worker")
	if !strings.Contains(res.cmdErr, "pane mirror-agent is not tracked by the remote mirror manager") {
		t.Fatalf("split error = %q, want untracked mirror pane", res.cmdErr)
	}
	assertPaneCount(t, sess, 1)
}

func seedMirroredCreatePaneWindow(t *testing.T, sess *Session, trackWindow, trackPane bool) *mux.Pane {
	t.Helper()

	pane := newTestPane(sess, 1, "mirror-agent")
	pane.Meta.Host = remoteMirrorCreatePaneTestHost
	w := newTestWindowWithPanes(t, sess, 1, "mirror-window", pane)
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, pane)

	mustSessionMutation(t, sess, func(sess *Session) {
		if sess.windowMirrorSigs == nil {
			sess.windowMirrorSigs = make(map[uint32]string)
		}
		sess.windowMirrorSigs[w.ID] = "signature"
		ref := mirrorpkg.WindowRef{Host: remoteMirrorCreatePaneTestHost, Session: "main", WindowName: "remote-main"}
		if trackWindow {
			if err := sess.mirror.TrackWindow(w.ID, ref, w.Width, w.Height); err != nil {
				t.Fatalf("TrackWindow: %v", err)
			}
		}
		if trackPane {
			err := sess.trackMirrorPane(pane, checkpoint.RemoteRef{Host: remoteMirrorCreatePaneTestHost, PaneName: "remote-agent"})
			if err != nil {
				t.Fatalf("trackMirrorPane: %v", err)
			}
		}
	})
	return pane
}

func assertPaneCount(t *testing.T, sess *Session, want int) {
	t.Helper()

	got := mustSessionQuery(t, sess, func(sess *Session) int { return len(sess.Panes) })
	if got != want {
		t.Fatalf("pane count = %d, want %d", got, want)
	}
}
