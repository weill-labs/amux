package test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/server"
	mirrorpkg "github.com/weill-labs/amux/internal/server/mirror"
)

func TestMirrorManagerHarnessPairHappyPath(t *testing.T) {
	t.Parallel()

	local, remote := newServerHarnessPair(t)
	_ = local
	remote.runCmd("rename", "pane-1", "remote-agent")

	pane := mux.NewProxyPaneWithScrollback(1, mux.PaneMeta{Name: "mirror", Host: "remote"}, 80, 23, mux.DefaultScrollbackLines, nil, nil, func(data []byte) (int, error) {
		return len(data), nil
	})
	t.Cleanup(func() {
		_ = pane.Close()
		_ = pane.WaitClosed()
	})

	mgr := mirrorpkg.NewManager(mirrorpkg.Config{
		Hosts: map[string]config.Host{
			"remote": {
				SSH:        "ignored",
				Session:    remote.session,
				SocketPath: server.SocketPath(remote.session),
			},
		},
		Dialer: mirrorHarnessDialer{},
	})
	t.Cleanup(mgr.Close)

	if err := mgr.Track(pane, checkpoint.RemoteRef{Host: "remote", Session: remote.session, PaneName: "remote-agent"}); err != nil {
		t.Fatalf("Track: %v", err)
	}
	waitForHarnessMirrorState(t, mgr, pane.ID, mirrorpkg.StateConnected)

	remote.runCmd("send-keys", "remote-agent", "printf MIRROR_PAIR_OUTPUT", "Enter")
	waitUntilHarnessMirror(t, func() bool {
		return pane.ScreenContains("MIRROR_PAIR_OUTPUT")
	})

	if _, err := pane.Write([]byte("printf MIRROR_PAIR_INPUT\r")); err != nil {
		t.Fatalf("mirror pane Write: %v", err)
	}
	remote.runCmd("wait", "content", "remote-agent", "MIRROR_PAIR_INPUT", "--timeout", "5s")
}

type mirrorHarnessDialer struct{}

func (mirrorHarnessDialer) Dial(ctx context.Context, host config.Host) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", host.SocketPath)
}

func waitForHarnessMirrorState(t *testing.T, mgr *mirrorpkg.Manager, paneID uint32, want mirrorpkg.State) {
	t.Helper()
	waitUntilHarnessMirror(t, func() bool {
		snap, ok := mgr.Snapshot(paneID)
		return ok && snap.State == want
	})
}

func waitUntilHarnessMirror(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met")
}
