package test

import (
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
	for _, want := range []string{"NAME", "PANES", "INDEX"} {
		if !strings.Contains(winsOut, want) {
			t.Fatalf("remote windows missing %q:\n%s", want, winsOut)
		}
	}

	// Attach the remote window by index -> a 2-pane local mirror window.
	attachOut := pair.local.runCmd("remote", "attach-window", remoteCLITestHost+":1")
	if !strings.Contains(attachOut, "as window") || !strings.Contains(attachOut, "2 panes") {
		t.Fatalf("attach-window output = %q", attachOut)
	}

	// attach-window is synchronous: both mirror panes exist once it returns.
	mirrors := localMirrorNames(pair.local.runCmd("list", "--no-cwd"), remoteCLITestHost)
	if len(mirrors) != 2 {
		t.Fatalf("expected 2 local mirror panes, got %d: %v", len(mirrors), mirrors)
	}

	// Output from the remote pane streams into the activated mirror window.
	pair.remote.runCmd("send-keys", "remote-side", "printf REMOTE_WIN_ATTACH", "Enter")
	if !pair.local.waitForFunc(func(s string) bool {
		return strings.Contains(s, "REMOTE_WIN_ATTACH")
	}, 5*time.Second) {
		t.Fatalf("mirrored output not observed in window capture")
	}
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
