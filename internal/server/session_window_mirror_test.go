package server

import (
	"testing"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/server/mirror"
)

// TestPushMirrorWindowSize covers forwarding a local mirror window's size to the
// remote (and the non-mirror-window early return).
func TestPushMirrorWindowSize(t *testing.T) {
	t.Parallel()

	mgr := mirror.NewManager(mirror.Config{})
	t.Cleanup(mgr.Close)
	if err := mgr.TrackWindow(5, mirror.WindowRef{Host: "h", Session: "main", WindowName: "amux"}, 80, 24); err != nil {
		t.Fatalf("TrackWindow: %v", err)
	}
	s := &Session{mirror: mgr, windowMirrorSigs: map[uint32]string{5: "sig"}}

	// Non-mirror window: no-op (size stays at the tracked 80x24).
	s.pushMirrorWindowSize(&mux.Window{ID: 99, Width: 150, Height: 40})
	if info := mgr.WindowMirrorInfos()[5]; info.Cols != 80 || info.Rows != 24 {
		t.Fatalf("unexpected size change for non-mirror window: %+v", info)
	}

	// Mirror window: size is forwarded to the manager.
	s.pushMirrorWindowSize(&mux.Window{ID: 5, Width: 150, Height: 40})
	if info := mgr.WindowMirrorInfos()[5]; info.Cols != 150 || info.Rows != 40 {
		t.Fatalf("size = %dx%d, want 150x40", info.Cols, info.Rows)
	}
}

// TestWindowMirrorCheckpointAndRestore covers the checkpoint capture and restore
// of window-layout subscriptions. The host is left unconfigured so the mirror
// stays pending (no dialing) while the map bookkeeping is exercised.
func TestWindowMirrorCheckpointAndRestore(t *testing.T) {
	t.Parallel()

	srcMgr := mirror.NewManager(mirror.Config{})
	t.Cleanup(srcMgr.Close)
	if err := srcMgr.TrackWindow(7, mirror.WindowRef{Host: "h", Session: "main", WindowName: "amux"}, 200, 50); err != nil {
		t.Fatalf("TrackWindow: %v", err)
	}
	src := &Session{mirror: srcMgr, windowMirrorSigs: map[uint32]string{7: "(0[a][b])"}}

	cps := windowMirrorCheckpoints(src)
	got, ok := cps[7]
	if !ok {
		t.Fatalf("window mirror 7 missing from checkpoint: %+v", cps)
	}
	want := checkpoint.RemoteWindowRef{Host: "h", Session: "main", WindowName: "amux", Cols: 200, Rows: 50, Signature: "(0[a][b])"}
	if got != want {
		t.Fatalf("checkpoint = %+v, want %+v", got, want)
	}

	dstMgr := mirror.NewManager(mirror.Config{})
	t.Cleanup(dstMgr.Close)
	dst := &Session{mirror: dstMgr}
	dst.restoreWindowMirrors(cps)

	if dst.windowMirrorSigs[7] != "(0[a][b])" {
		t.Fatalf("restored signature = %q, want %q", dst.windowMirrorSigs[7], "(0[a][b])")
	}
	if _, ok := dstMgr.WindowSnapshot(7); !ok {
		t.Fatal("window mirror was not re-tracked on restore")
	}
}
