package test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/server"
)

func TestRemoteAttachChooserSelectsMirror(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available for fake ssh transport")
	}

	remote := newServerHarnessForSession(t, newServerHarnessPairSession(t, "remote"), "", 80, 24, "", false, false)
	remote.runCmd("rename", "pane-1", "remote-agent")
	remote.runCmd("spawn", "--name", "remote-side", "--vertical")

	fakeDir, sshLog := writeFakeSSH(t)
	configPath := writeRemoteChooserConfig(t, remote)
	local := newAmuxHarness(t,
		"AMUX_CONFIG="+configPath,
		"PATH="+fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AMUX_FAKE_SSH_LOG="+sshLog,
	)

	out := local.runCmd("remote", "attach", remoteCLITestHost)
	if !strings.Contains(out, "Opened remote pane chooser for "+remoteCLITestHost) {
		t.Fatalf("remote attach chooser output = %q", out)
	}
	waitForFakeSSHCalls(t, sshLog, 1)
	if !local.waitFor("Remote panes: "+remoteCLITestHost, 5*time.Second) || !local.waitFor("remote-side", 5*time.Second) {
		t.Fatalf("remote chooser did not render remote panes\nouter:\n%s", local.captureOuter())
	}

	gen := local.generation()
	local.sendClientKeys("r", "e", "m", "o", "t", "e", "-", "s", "i", "d", "e", "Enter")
	local.waitLayout(gen)
	mirrorName := waitForInnerMirrorName(t, local, remoteCLITestHost)
	waitForFakeSSHCalls(t, sshLog, 3)

	remote.runCmd("send-keys", "remote-side", "printf REMOTE_CHOOSER_ATTACH", "Enter")
	if !local.waitFor("REMOTE_CHOOSER_ATTACH", 5*time.Second) {
		t.Fatalf("selected mirror %s did not receive remote output\nouter:\n%s", mirrorName, local.captureOuter())
	}
}

func writeRemoteChooserConfig(t *testing.T, remote *ServerHarness) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.toml")
	configContent := fmt.Sprintf(`
[remote.hosts.%s]
ssh = "fake-target"
session = %q
socket_path = %q
`, remoteCLITestHost, remote.session, server.SocketPath(remote.session))
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("writing remote chooser config: %v", err)
	}
	return configPath
}

func waitForInnerMirrorName(t *testing.T, h *AmuxHarness, host string) string {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if name := localMirrorName(h.runCmd("list", "--no-cwd"), host); name != "" {
			return name
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("local mirror for host %q did not appear", host)
	return ""
}
