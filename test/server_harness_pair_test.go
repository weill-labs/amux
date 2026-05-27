package test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/server"
)

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
	for key := range harnessBlockedEnvKeys {
		if value, ok := harnessEnvValue(h.cmd.Env, key); ok {
			t.Fatalf("%s server env leaked %s=%q", label, key, value)
		}
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
