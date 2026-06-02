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

const remoteCLITestHost = "hetzner-1"

func TestRemoteCLIAddListRemove(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	configPath := filepath.Join(h.home, ".config", "amux", "config.toml")

	addOut := h.runCmd("remote", "add", remoteCLITestHost,
		"--ssh", "fake-target",
		"--socket", "/tmp/amux-1000/main",
		"--session", "main",
	)
	if !strings.Contains(addOut, "Added remote "+remoteCLITestHost) {
		t.Fatalf("remote add output = %q", addOut)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("reading config after remote add: %v", err)
	}
	for _, want := range []string{
		"[remote.hosts." + remoteCLITestHost + "]",
		`ssh = "fake-target"`,
		`session = "main"`,
		`socket_path = "/tmp/amux-1000/main"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("config missing %q:\n%s", want, string(data))
		}
	}

	listOut := h.runCmd("remote", "list")
	for _, want := range []string{remoteCLITestHost, "fake-target", "/tmp/amux-1000/main"} {
		if !strings.Contains(listOut, want) {
			t.Fatalf("remote list missing %q:\n%s", want, listOut)
		}
	}

	rmOut := h.runCmd("remote", "rm", remoteCLITestHost)
	if !strings.Contains(rmOut, "Removed remote "+remoteCLITestHost) {
		t.Fatalf("remote rm output = %q", rmOut)
	}
	listOut = h.runCmd("remote", "list")
	if !strings.Contains(listOut, "No remotes.") {
		t.Fatalf("remote list after rm = %q, want no remotes", listOut)
	}
}

func TestRemoteCLIPanesAttachDetachResizeAndLocalKill(t *testing.T) {
	t.Parallel()

	pair := newRemoteCLIPair(t)
	pair.remote.runCmd("rename", "pane-1", "remote-agent")
	pair.remote.runCmd("spawn", "--name", "remote-side", "--vertical")

	panesOut := pair.local.runCmd("remote", "panes", remoteCLITestHost)
	for _, want := range []string{"REF", "PANE", "remote-agent", "remote-side"} {
		if !strings.Contains(panesOut, want) {
			t.Fatalf("remote panes missing %q:\n%s", want, panesOut)
		}
	}
	remoteSideRef := remoteObjectRefFromList(t, panesOut, "remote-side")

	attachOut := pair.local.runCmd("remote", "attach", remoteSideRef)
	if !strings.Contains(attachOut, "Attached "+remoteCLITestHost+":remote-side") {
		t.Fatalf("remote attach output = %q", attachOut)
	}
	mirrorName := waitForLocalMirrorName(t, pair.local, remoteCLITestHost)
	waitForFakeSSHCalls(t, pair.sshLog, 3)

	pair.remote.runCmd("send-keys", "remote-side", "printf REMOTE_CLI_ATTACH", "Enter")
	pair.local.waitForTimeout(mirrorName, "REMOTE_CLI_ATTACH", "5s")

	beforeResizeCalls := countFakeSSHCalls(t, pair.sshLog)
	resizeOut := pair.local.runCmd("remote", "resize", mirrorName)
	if !strings.Contains(resizeOut, "Resized remote pane "+remoteCLITestHost+":remote-side") {
		t.Fatalf("remote resize output = %q", resizeOut)
	}
	waitForFakeSSHCalls(t, pair.sshLog, beforeResizeCalls+1)

	detachOut := pair.local.runCmd("remote", "detach", mirrorName)
	if !strings.Contains(detachOut, "Detached mirror "+mirrorName) {
		t.Fatalf("remote detach output = %q", detachOut)
	}
	if listOut := pair.local.runCmd("list", "--no-cwd"); strings.Contains(listOut, mirrorName) {
		t.Fatalf("detached mirror still in local list:\n%s", listOut)
	}
	if remoteList := pair.remote.runCmd("list", "--no-cwd"); !strings.Contains(remoteList, "remote-side") {
		t.Fatalf("remote detach removed remote pane:\n%s", remoteList)
	}

	attachOut = pair.local.runCmd("remote", "attach", remoteSideRef)
	if !strings.Contains(attachOut, "Attached "+remoteCLITestHost+":remote-side") {
		t.Fatalf("second remote attach output = %q", attachOut)
	}
	mirrorName = waitForLocalMirrorName(t, pair.local, remoteCLITestHost)
	killOut := pair.local.runCmd("kill", mirrorName)
	if !strings.Contains(killOut, "Killed "+mirrorName) {
		t.Fatalf("local kill output = %q", killOut)
	}
	if remoteList := pair.remote.runCmd("list", "--no-cwd"); !strings.Contains(remoteList, "remote-side") {
		t.Fatalf("kill without --remote removed remote pane:\n%s", remoteList)
	}

	attachOut = pair.local.runCmd("remote", "attach", remoteSideRef)
	if !strings.Contains(attachOut, "Attached "+remoteCLITestHost+":remote-side") {
		t.Fatalf("third remote attach output = %q", attachOut)
	}
	mirrorName = waitForLocalMirrorName(t, pair.local, remoteCLITestHost)
	killOut = pair.local.runCmd("kill", "--remote", mirrorName)
	if !strings.Contains(killOut, "Killed remote pane "+remoteCLITestHost+":remote-side") {
		t.Fatalf("remote kill output = %q", killOut)
	}
	if localList := pair.local.runCmd("list", "--no-cwd"); strings.Contains(localList, mirrorName) {
		t.Fatalf("kill --remote left mirror in local list:\n%s", localList)
	}
	if remoteList := pair.remote.runCmd("list", "--no-cwd"); strings.Contains(remoteList, "remote-side") {
		t.Fatalf("kill --remote left remote pane running:\n%s", remoteList)
	}
}

type remoteCLIPair struct {
	local  *ServerHarness
	remote *ServerHarness
	sshLog string
}

func newRemoteCLIPair(t *testing.T) remoteCLIPair {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available for fake ssh transport")
	}

	remote := newServerHarnessForSession(t, newServerHarnessPairSession(t, "remote"), "", 80, 24, "", false, false)
	fakeDir, sshLog := writeFakeSSH(t)
	configContent := fmt.Sprintf(`
[remote.hosts.%s]
ssh = "fake-target"
session = %q
socket_path = %q
`, remoteCLITestHost, remote.session, server.SocketPath(remote.session))
	pathEnv := "PATH=" + fakeDir + string(os.PathListSeparator) + os.Getenv("PATH")
	local := newServerHarnessForSession(t, newServerHarnessPairSession(t, "local"), "", 100, 24, configContent, false, false,
		pathEnv,
		"AMUX_FAKE_SSH_LOG="+sshLog,
	)
	return remoteCLIPair{local: local, remote: remote, sshLog: sshLog}
}

func writeFakeSSH(t *testing.T) (dir, logPath string) {
	t.Helper()
	dir = t.TempDir()
	logPath = filepath.Join(dir, "ssh.log")
	relayPath := filepath.Join(dir, "relay.py")
	relay := `import os
import selectors
import socket
import sys

sock_path = sys.argv[1]
conn = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
conn.connect(sock_path)
conn.setblocking(False)
selector = selectors.DefaultSelector()
selector.register(0, selectors.EVENT_READ, "stdin")
selector.register(conn, selectors.EVENT_READ, "socket")
while True:
    events = selector.select()
    for key, _ in events:
        if key.data == "stdin":
            data = os.read(0, 65536)
            if not data:
                selector.unregister(0)
                try:
                    conn.shutdown(socket.SHUT_WR)
                except OSError:
                    pass
                continue
            conn.sendall(data)
        else:
            data = conn.recv(65536)
            if not data:
                sys.exit(0)
            os.write(1, data)
`
	if err := os.WriteFile(relayPath, []byte(relay), 0o644); err != nil {
		t.Fatalf("writing fake ssh relay: %v", err)
	}
	scriptPath := filepath.Join(dir, "ssh")
	script := fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
printf '%%s\n' "$*" >> "${AMUX_FAKE_SSH_LOG:?}"
sock="${@: -1}"
exec python3 %q "$sock"
`, relayPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake ssh: %v", err)
	}
	return dir, logPath
}

func waitForLocalMirrorName(t *testing.T, h *ServerHarness, host string) string {
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

func localMirrorName(listOut, host string) string {
	for _, line := range strings.Split(listOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[2] == host {
			return fields[1]
		}
	}
	return ""
}

func remoteObjectRefFromList(t *testing.T, listOut, rowText string) string {
	t.Helper()
	for _, line := range strings.Split(listOut, "\n") {
		if !strings.Contains(line, rowText) {
			continue
		}
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "amux://") {
				return field
			}
		}
		t.Fatalf("line containing %q has no canonical ref:\n%s", rowText, line)
	}
	t.Fatalf("no line containing %q in:\n%s", rowText, listOut)
	return ""
}

func waitForFakeSSHCalls(t *testing.T, logPath string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if countFakeSSHCalls(t, logPath) >= want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("fake ssh call count = %d, want at least %d", countFakeSSHCalls(t, logPath), want)
}

func countFakeSSHCalls(t *testing.T, logPath string) int {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("reading fake ssh log: %v", err)
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}
