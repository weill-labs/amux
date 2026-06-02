package test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/server"
)

// newServerHarnessPair starts two independent amux servers in the same test.
// The "local" and "remote" labels are conventional; no protocol behavior is
// attached to them. A separate persistent pair helper is intentionally omitted:
// this constructor already uses exitUnattached=false, matching the current
// persistent harness lifetime semantics.
func newServerHarnessPair(tb testing.TB) (local, remote *ServerHarness) {
	tb.Helper()

	cleanup := &serverHarnessPairCleanup{}
	// Register first so LIFO cleanup runs leak verification after pair members
	// have been added and after both per-harness server cleanups finish.
	tb.Cleanup(func() {
		cleanup.verifyNoLeaks(tb)
	})

	local = newServerHarnessPairMember(tb, "local")
	cleanup.add("local", local)
	remote = newServerHarnessPairMember(tb, "remote")
	cleanup.add("remote", remote)

	tb.Cleanup(func() {
		detachServerHarnessClients(local)
		detachServerHarnessClients(remote)
	})

	return local, remote
}

func newServerHarnessPairMember(tb testing.TB, label string) *ServerHarness {
	tb.Helper()
	return newServerHarnessForSession(tb, newServerHarnessPairSession(tb, label), "", 80, 24, "", false, false)
}

func newServerHarnessPairSession(tb testing.TB, label string) string {
	tb.Helper()

	var entropy [8]byte
	mustRandRead(tb, entropy[:])
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%x", tb.Name(), label, entropy)))
	return fmt.Sprintf("t-%x", sum[:8])
}

func detachServerHarnessClients(h *ServerHarness) {
	if h == nil {
		return
	}
	if h.keepalive != nil {
		h.keepalive.close()
		h.keepalive = nil
	}
	if h.client != nil {
		h.client.close()
		h.client = nil
	}
}

type serverHarnessPairCleanup struct {
	entries []serverHarnessPairCleanupEntry
}

type serverHarnessPairCleanupEntry struct {
	label      string
	session    string
	socketPath string
	serverPID  int
}

func (c *serverHarnessPairCleanup) add(label string, h *ServerHarness) {
	if h == nil {
		return
	}
	entry := serverHarnessPairCleanupEntry{
		label:      label,
		session:    h.session,
		socketPath: server.SocketPath(h.session),
	}
	if h.cmd != nil && h.cmd.Process != nil {
		entry.serverPID = h.cmd.Process.Pid
	}
	c.entries = append(c.entries, entry)
}

func (c *serverHarnessPairCleanup) verifyNoLeaks(tb testing.TB) {
	tb.Helper()
	if tb.Failed() {
		return
	}
	for _, entry := range c.entries {
		waitForNoHarnessSocketFiles(tb, entry)
		waitForNoHarnessProcessGroupMembers(tb, entry)
	}
}

func waitForNoHarnessSocketFiles(tb testing.TB, entry serverHarnessPairCleanupEntry) {
	tb.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for {
		leaked := harnessSocketFiles(entry.session, entry.socketPath)
		if len(leaked) == 0 {
			return
		}
		if !time.Now().Before(deadline) {
			tb.Fatalf("%s harness leaked socket files after cleanup: %s", entry.label, strings.Join(leaked, ", "))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func harnessSocketFiles(session, socketPath string) []string {
	var paths []string
	for _, path := range []string{
		socketPath,
		filepath.Join(server.SocketDir(), session+".pprof"),
		filepath.Join(server.SocketDir(), session+".client.pprof"),
	} {
		if isSocketFile(path) {
			paths = append(paths, path)
		}
	}
	if matches, err := filepath.Glob(filepath.Join(server.SocketDir(), session+".client.*.pprof")); err == nil {
		for _, path := range matches {
			if isSocketFile(path) {
				paths = append(paths, path)
			}
		}
	}
	return paths
}

func isSocketFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

func waitForNoHarnessProcessGroupMembers(tb testing.TB, entry serverHarnessPairCleanupEntry) {
	tb.Helper()
	if entry.serverPID <= 0 {
		return
	}
	if _, err := exec.LookPath("pgrep"); err != nil {
		tb.Logf("pgrep not available; skipping process-group leak check for %s harness", entry.label)
		return
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		members := processGroupMembers(entry.serverPID)
		if len(members) == 0 {
			return
		}
		if !time.Now().Before(deadline) {
			tb.Fatalf("%s harness leaked process group members after cleanup: %v", entry.label, members)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func processGroupMembers(pgid int) []int {
	if pgid <= 0 {
		return nil
	}
	out, err := exec.Command("pgrep", "-g", strconv.Itoa(pgid)).Output()
	if err != nil {
		return nil
	}

	var members []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err == nil {
			members = append(members, pid)
		}
	}
	return members
}

func TestServerHarnessPair_Independent(t *testing.T) {
	t.Parallel()

	local, remote := newServerHarnessPair(t)
	assertServerHarnessPairIsolated(t, local, remote)

	local.runCmd("spawn", "--name", "L1")
	remote.runCmd("spawn", "--name", "R1")

	localList := local.runCmd("list")
	if !strings.Contains(localList, "L1") {
		t.Fatalf("local list missing L1:\n%s", localList)
	}
	if strings.Contains(localList, "R1") {
		t.Fatalf("local list leaked remote pane R1:\n%s", localList)
	}

	remoteList := remote.runCmd("list")
	if !strings.Contains(remoteList, "R1") {
		t.Fatalf("remote list missing R1:\n%s", remoteList)
	}
	if strings.Contains(remoteList, "L1") {
		t.Fatalf("remote list leaked local pane L1:\n%s", remoteList)
	}
}

func assertServerHarnessPairIsolated(t *testing.T, local, remote *ServerHarness) {
	t.Helper()
	if local == nil || remote == nil {
		t.Fatalf("newServerHarnessPair returned local=%v remote=%v, want two harnesses", local, remote)
	}
	if local == remote {
		t.Fatal("newServerHarnessPair returned the same harness for local and remote")
	}

	assertDistinctHarnessPath(t, "session", local.session, remote.session)
	assertDistinctHarnessPath(t, "home", local.home, remote.home)
	assertDistinctHarnessPath(t, "socket", server.SocketPath(local.session), server.SocketPath(remote.session))
	assertDistinctHarnessPath(t, "log", local.logPath, remote.logPath)
	if local.coverDir != "" || remote.coverDir != "" {
		assertDistinctHarnessPath(t, "coverage", local.coverDir, remote.coverDir)
	}

	assertHarnessEnvScrubbed(t, "local", local)
	assertHarnessEnvScrubbed(t, "remote", remote)
}

func assertDistinctHarnessPath(t *testing.T, label, left, right string) {
	t.Helper()
	if left == "" || right == "" {
		t.Fatalf("%s paths must be non-empty: %q %q", label, left, right)
	}
	if filepath.Clean(left) == filepath.Clean(right) {
		t.Fatalf("%s paths should be distinct, both were %q", label, left)
	}
}

func assertHarnessEnvScrubbed(t *testing.T, label string, h *ServerHarness) {
	t.Helper()
	if h == nil || h.cmd == nil {
		t.Fatalf("%s server harness command is nil", label)
	}
	for key := range harnessBlockedEnvKeys {
		if key == proto.SocketDirEnv {
			continue
		}
		if value, ok := harnessEnvValue(h.cmd.Env, key); ok {
			t.Fatalf("%s server env leaked %s=%q", label, key, value)
		}
	}
	if got, ok := harnessEnvValue(h.cmd.Env, proto.SocketDirEnv); !ok || filepath.Clean(got) != filepath.Clean(testSocketDir()) {
		t.Fatalf("%s server %s = %q, want %q", label, proto.SocketDirEnv, got, testSocketDir())
	}
	if got, ok := harnessEnvValue(h.cmd.Env, "HOME"); !ok || filepath.Clean(got) != filepath.Clean(h.home) {
		t.Fatalf("%s server HOME = %q, want %q", label, got, h.home)
	}
	if got, ok := harnessEnvValue(h.cmd.Env, "AMUX_LOG_DIR"); !ok || filepath.Clean(got) != filepath.Clean(testLogDir(h.home)) {
		t.Fatalf("%s server AMUX_LOG_DIR = %q, want %q", label, got, testLogDir(h.home))
	}
}

func harnessEnvValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix), true
		}
	}
	return "", false
}
