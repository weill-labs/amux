package test

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRemoteCLIAttachWindow mirrors a whole remote window (two panes in a split)
// into a new local window and verifies output flows from a mirrored pane.
func TestRemoteCLIAttachWindow(t *testing.T) {
	t.Parallel()

	pair := newRemoteCLIPair(t)
	pair.remote.runCmd("rename", "pane-1", "remote-agent")
	pair.remote.runCmd("spawn", "--name", "remote-side", "--vertical")

	// remote windows lists the remote host's windows.
	winsOut := pair.local.runCmd("remote", "windows", remoteCLITestHost)
	for _, want := range []string{"REF", "NAME", "PANES", "INDEX", remoteWindowIndexRef(pair, 1)} {
		if !strings.Contains(winsOut, want) {
			t.Fatalf("remote windows missing %q:\n%s", want, winsOut)
		}
	}

	// Attach the remote window by index -> a 2-pane local mirror window.
	attachOut := pair.local.runCmd("remote", "attach-window", remoteWindowIndexRef(pair, 1))
	if !strings.Contains(attachOut, "as window") || !strings.Contains(attachOut, "2 panes") {
		t.Fatalf("attach-window output = %q", attachOut)
	}

	// attach-window is synchronous: both mirror panes exist once it returns.
	mirrors := localMirrorNames(pair.local.runCmd("list", "--no-cwd"), remoteCLITestHost)
	if len(mirrors) != 2 {
		t.Fatalf("expected 2 local mirror panes, got %d: %v", len(mirrors), mirrors)
	}
	remoteSideMirror := localMirrorNameForRemotePane(t, pair.local, remoteCLITestHost, "remote-side")

	// Output from the remote pane streams into the activated mirror window.
	pair.remote.runCmd("send-keys", "remote-side", "printf REMOTE_WIN_ATTACH", "Enter")
	pair.local.waitForTimeout(remoteSideMirror, "REMOTE_WIN_ATTACH", "5s")
}

// TestRemoteCLIAttachWindowDynamicResync verifies the local mirror window tracks
// the remote window's structure: adding a pane on the remote grows the mirror,
// and removing one shrinks it.
func TestRemoteCLIAttachWindowDynamicResync(t *testing.T) {
	t.Parallel()

	pair := newRemoteCLIPair(t)
	pair.remote.runCmd("spawn", "--name", "remote-side", "--vertical")

	attachOut := pair.local.runCmd("remote", "attach-window", remoteWindowIndexRef(pair, 1))
	if !strings.Contains(attachOut, "2 panes") {
		t.Fatalf("attach-window output = %q", attachOut)
	}
	if got := len(localMirrorNames(pair.local.runCmd("list", "--no-cwd"), remoteCLITestHost)); got != 2 {
		t.Fatalf("initial mirror pane count = %d, want 2", got)
	}

	// Adding a remote pane grows the local mirror to 3.
	pair.remote.runCmd("spawn", "--name", "remote-third", "--vertical")
	if !pair.local.waitForFunc(func(string) bool {
		return len(localMirrorNames(pair.local.runCmd("list", "--no-cwd"), remoteCLITestHost)) == 3
	}, 5*time.Second) {
		t.Fatalf("resync did not grow local mirror to 3 panes")
	}

	// Removing a remote pane shrinks the local mirror back to 2.
	pair.remote.runCmd("kill", "remote-third")
	if !pair.local.waitForFunc(func(string) bool {
		return len(localMirrorNames(pair.local.runCmd("list", "--no-cwd"), remoteCLITestHost)) == 2
	}, 5*time.Second) {
		t.Fatalf("resync did not shrink local mirror to 2 panes")
	}
}

func TestRemoteCLISpawnWindowForwardsToMirroredWindow(t *testing.T) {
	t.Parallel()

	pair := newRemoteCLIPair(t)
	pair.remote.runCmd("rename", "pane-1", "remote-agent")

	attachOut := pair.local.runCmd("remote", "attach-window", remoteWindowIndexRef(pair, 1))
	winName := windowNameFromAttach(attachOut)
	if winName == "" {
		t.Fatalf("could not parse window name from %q", attachOut)
	}

	out := pair.local.runCmd("spawn", "--window", winName, "--name", "remote-by-window")
	if !strings.Contains(out, "Spawned remote-by-window") {
		t.Fatalf("spawn --window output = %q", out)
	}
	if remoteList := pair.remote.runCmd("list", "--no-cwd"); !strings.Contains(remoteList, "remote-by-window") {
		t.Fatalf("spawn --window did not create a remote pane:\n%s", remoteList)
	}
	if !pair.local.waitForFunc(func(string) bool {
		return len(localMirrorNames(pair.local.runCmd("list", "--no-cwd"), remoteCLITestHost)) == 2
	}, 5*time.Second) {
		t.Fatalf("local mirror did not reconcile the remote spawn")
	}
}

func TestRemoteCLISpawnAtMirrorPaneForwardsToRemotePane(t *testing.T) {
	t.Parallel()

	pair := newRemoteCLIPair(t)
	pair.remote.runCmd("rename", "pane-1", "remote-agent")

	attachOut := pair.local.runCmd("remote", "attach-window", remoteWindowIndexRef(pair, 1))
	if !strings.Contains(attachOut, "1 panes") {
		t.Fatalf("attach-window output = %q", attachOut)
	}
	mirrors := localMirrorNames(pair.local.runCmd("list", "--no-cwd"), remoteCLITestHost)
	if len(mirrors) != 1 {
		t.Fatalf("initial mirror panes = %v, want one", mirrors)
	}

	out := pair.local.runCmd("spawn", "--at", mirrors[0], "--name", "remote-by-at")
	if !strings.Contains(out, "Spawned remote-by-at") {
		t.Fatalf("spawn --at mirror output = %q", out)
	}
	if remoteList := pair.remote.runCmd("list", "--no-cwd"); !strings.Contains(remoteList, "remote-by-at") {
		t.Fatalf("spawn --at mirror did not create a remote pane:\n%s", remoteList)
	}
	if !pair.local.waitForFunc(func(string) bool {
		return len(localMirrorNames(pair.local.runCmd("list", "--no-cwd"), remoteCLITestHost)) == 2
	}, 5*time.Second) {
		t.Fatalf("local mirror did not reconcile the remote spawn")
	}
}

func TestRemoteCLISpawnActiveMirrorWindowForwardsToRemotePane(t *testing.T) {
	t.Parallel()

	pair := newRemoteCLIPair(t)
	pair.remote.runCmd("rename", "pane-1", "remote-agent")

	attachOut := pair.local.runCmd("remote", "attach-window", remoteWindowIndexRef(pair, 1))
	if !strings.Contains(attachOut, "1 panes") {
		t.Fatalf("attach-window output = %q", attachOut)
	}

	out := pair.local.runCmd("spawn", "--name", "remote-by-active")
	if !strings.Contains(out, "Spawned remote-by-active") {
		t.Fatalf("spawn active mirror output = %q", out)
	}
	if remoteList := pair.remote.runCmd("list", "--no-cwd"); !strings.Contains(remoteList, "remote-by-active") {
		t.Fatalf("spawn active mirror did not create a remote pane:\n%s", remoteList)
	}
	if !pair.local.waitForFunc(func(string) bool {
		return len(localMirrorNames(pair.local.runCmd("list", "--no-cwd"), remoteCLITestHost)) == 2
	}, 5*time.Second) {
		t.Fatalf("local mirror did not reconcile the active mirror spawn")
	}
}

func TestRemoteCLISplitMirrorPaneForwardsToRemotePane(t *testing.T) {
	t.Parallel()

	pair := newRemoteCLIPair(t)
	pair.remote.runCmd("rename", "pane-1", "remote-agent")

	attachOut := pair.local.runCmd("remote", "attach-window", remoteWindowIndexRef(pair, 1))
	if !strings.Contains(attachOut, "1 panes") {
		t.Fatalf("attach-window output = %q", attachOut)
	}
	mirrors := localMirrorNames(pair.local.runCmd("list", "--no-cwd"), remoteCLITestHost)
	if len(mirrors) != 1 {
		t.Fatalf("initial mirror panes = %v, want one", mirrors)
	}

	out := pair.local.runControlCmd("split", mirrors[0], "--name", "remote-by-split")
	if !strings.Contains(out, "Split horizontal: new pane remote-by-split") {
		t.Fatalf("split mirror output = %q", out)
	}
	if remoteList := pair.remote.runCmd("list", "--no-cwd"); !strings.Contains(remoteList, "remote-by-split") {
		t.Fatalf("split mirror did not create a remote pane:\n%s", remoteList)
	}
	if !pair.local.waitForFunc(func(string) bool {
		return len(localMirrorNames(pair.local.runCmd("list", "--no-cwd"), remoteCLITestHost)) == 2
	}, 5*time.Second) {
		t.Fatalf("local mirror did not reconcile the remote split")
	}
}

// TestRemoteCLIDetachWindow tears down a mirrored window and verifies the local
// panes are gone while the remote panes survive.
func TestRemoteCLIDetachWindow(t *testing.T) {
	t.Parallel()

	pair := newRemoteCLIPair(t)
	pair.remote.runCmd("spawn", "--name", "remote-side", "--vertical")

	attachOut := pair.local.runCmd("remote", "attach-window", remoteWindowIndexRef(pair, 1))
	winName := windowNameFromAttach(attachOut)
	if winName == "" {
		t.Fatalf("could not parse window name from %q", attachOut)
	}
	if got := len(localMirrorNames(pair.local.runCmd("list", "--no-cwd"), remoteCLITestHost)); got != 2 {
		t.Fatalf("expected 2 mirror panes, got %d", got)
	}

	detachOut := pair.local.runCmd("remote", "detach-window", winName)
	if !strings.Contains(detachOut, "Detached mirror window") {
		t.Fatalf("detach-window output = %q", detachOut)
	}
	if got := len(localMirrorNames(pair.local.runCmd("list", "--no-cwd"), remoteCLITestHost)); got != 0 {
		t.Fatalf("mirror panes remain after detach-window: %d", got)
	}
	if remoteList := pair.remote.runCmd("list", "--no-cwd"); !strings.Contains(remoteList, "remote-side") {
		t.Fatalf("detach-window destroyed remote panes:\n%s", remoteList)
	}
}

func windowNameFromAttach(out string) string {
	const marker = "as window "
	i := strings.Index(out, marker)
	if i < 0 {
		return ""
	}
	rest := out[i+len(marker):]
	if j := strings.Index(rest, " ("); j >= 0 {
		return rest[:j]
	}
	return strings.TrimSpace(rest)
}

func remoteWindowIndexRef(pair remoteCLIPair, index int) string {
	return "amux://" + remoteCLITestHost + "/" + pair.remote.session + "/window/index/" + strconv.Itoa(index)
}

func localMirrorNames(listOut, host string) []string {
	names := make([]string, 0)
	for _, line := range strings.Split(listOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[2] == host {
			names = append(names, fields[1])
		}
	}
	return names
}

func localMirrorNameForRemotePane(t *testing.T, h *ServerHarness, host, remotePane string) string {
	t.Helper()
	capture := h.captureJSON()
	for _, pane := range capture.Panes {
		if pane.Mirror != nil && pane.Mirror.Host == host && pane.Mirror.PaneName == remotePane {
			return pane.Name
		}
	}
	t.Fatalf("no local mirror for %s:%s in capture: %+v", host, remotePane, capture.Panes)
	return ""
}
