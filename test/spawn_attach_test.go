package test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/server"
)

func TestSpawnAttachCreatesMirrorAndForwardsInput(t *testing.T) {
	t.Parallel()
	skipIfNoNetcat(t)

	fakeSSHDir := writeSpawnAttachFakeSSH(t)
	remoteHost := "hetzner-1"
	local, remote := newServerHarnessPairWithLocalRemote(t, remoteHost, "PATH="+fakeSSHDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	remote.runCmd("rename", "pane-1", "pane-1786")
	out := local.runCmd("spawn", "--at", "pane-1", "--horizontal", "--attach", remoteHost+":pane-1786", "--name", "mirror-agent")
	if !strings.Contains(out, "Spawned mirror-agent") {
		t.Fatalf("spawn --attach output = %q, want spawned mirror-agent", out)
	}

	capture := local.captureJSON()
	if len(capture.Panes) != 2 {
		t.Fatalf("local pane count = %d, want 2", len(capture.Panes))
	}
	targetPane := local.jsonPane(capture, "pane-1")
	mirrorPane := local.jsonPane(capture, "mirror-agent")
	if mirrorPane.Host != remoteHost {
		t.Fatalf("mirror host = %q, want %q", mirrorPane.Host, remoteHost)
	}
	if targetPane.Position.X >= mirrorPane.Position.X || targetPane.Position.Y != mirrorPane.Position.Y {
		t.Fatalf("spawn --attach should create mirror next to target, target=%+v mirror=%+v", targetPane.Position, mirrorPane.Position)
	}

	remote.runCmd("send-keys", "pane-1786", "printf FROM_REMOTE", "Enter")
	if out := local.runCmd("wait", "content", "mirror-agent", "FROM_REMOTE", "--timeout", "5s"); strings.Contains(out, "timed out") {
		t.Fatalf("mirror did not receive remote output:\n%s", out)
	}

	local.runCmd("send-keys", "mirror-agent", "printf FROM_MIRROR", "Enter")
	if out := remote.runCmd("wait", "content", "pane-1786", "FROM_MIRROR", "--timeout", "5s"); strings.Contains(out, "timed out") {
		t.Fatalf("remote pane did not receive mirror input:\n%s", out)
	}
}

func TestSpawnAttachUnknownHostFails(t *testing.T) {
	t.Parallel()

	// No fake ssh / nc needed: the unknown host is rejected before any
	// connection is attempted.
	local, _ := newServerHarnessPairWithLocalRemote(t, "hetzner-1")

	out := local.runCmd("spawn", "--at", "pane-1", "--horizontal", "--attach", "nonexistent:pane-1786", "--name", "mirror-agent")
	if !strings.Contains(out, `remote "nonexistent" not found`) {
		t.Fatalf("spawn --attach unknown host output = %q, want a not-found error", out)
	}

	// The failure must be surfaced before any mirror pane is created — the
	// local session still has only its original pane (this is the silent
	// unknown-host regression guard).
	capture := local.captureJSON()
	if len(capture.Panes) != 1 {
		t.Fatalf("local pane count = %d, want 1 (no mirror created on error)", len(capture.Panes))
	}
}

func skipIfNoNetcat(tb testing.TB) {
	tb.Helper()
	if _, err := exec.LookPath("nc"); err != nil {
		tb.Skip("skipping: `nc` not on PATH (required by the fake-ssh mirror transport)")
	}
}

func newServerHarnessPairWithLocalRemote(tb testing.TB, hostName string, localExtraEnv ...string) (local, remote *ServerHarness) {
	tb.Helper()

	cleanup := &serverHarnessPairCleanup{}
	tb.Cleanup(func() {
		cleanup.verifyNoLeaks(tb)
	})

	remote = newServerHarnessPairMember(tb, "remote")
	cleanup.add("remote", remote)

	localConfig := fmt.Sprintf(`[remote.hosts.%s]
ssh = "test@example.invalid"
session = %q
socket_path = %q
`, hostName, remote.session, server.SocketPath(remote.session))
	local = newServerHarnessForSession(tb, newServerHarnessPairSession(tb, "local"), "", 80, 24, localConfig, false, false, localExtraEnv...)
	cleanup.add("local", local)

	tb.Cleanup(func() {
		detachServerHarnessClients(local)
		detachServerHarnessClients(remote)
	})

	return local, remote
}

func writeSpawnAttachFakeSSH(tb testing.TB) string {
	tb.Helper()

	dir := tb.TempDir()
	script := `#!/bin/sh
sock=
for arg in "$@"; do
  sock="$arg"
done
exec nc -U "$sock"
`
	if err := os.WriteFile(dir+"/ssh", []byte(script), 0755); err != nil {
		tb.Fatalf("writing fake ssh: %v", err)
	}
	return dir
}
