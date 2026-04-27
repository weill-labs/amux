package test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/server"
)

func TestSessionLockSurvivesHotReload(t *testing.T) {
	t.Parallel()

	h := newPersistentReloadHarness(t, privateAmuxBin(t))

	reloadGen := h.generation()
	h.runCmd("reload-server")
	h.waitForReloadedClient(reloadGen, 10*time.Second)

	newServerOut := runDuplicateServerProbe(t, h)
	if !strings.Contains(newServerOut, "server already running for session") || !strings.Contains(newServerOut, h.inner) {
		t.Fatalf("duplicate server probe output = %q, want already-running error for %q", strings.TrimSpace(newServerOut), h.inner)
	}

	lockPath := filepath.Join(server.SocketDir(), h.inner+".lock")
	if err := exec.Command("flock", "-n", lockPath, "true").Run(); err == nil {
		t.Fatalf("flock probe acquired %s after hot reload; want lock still held", lockPath)
	}
}

func runDuplicateServerProbe(t *testing.T, h *AmuxHarness) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, h.innerBin, "_server", h.inner)
	env := removeEnv(os.Environ(), "AMUX_SESSION")
	env = removeEnv(env, "TMUX")
	env = append(env,
		"HOME="+h.outer.home,
		"AMUX_NO_WATCH=1",
		"AMUX_DISABLE_META_REFRESH=1",
	)
	if h.outer.coverDir != "" {
		env = upsertEnv(env, "GOCOVERDIR", h.outer.coverDir)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("duplicate server probe timed out; process likely started unexpectedly\noutput:\n%s", string(out))
	}
	if err == nil {
		t.Fatalf("duplicate server probe exited successfully, want already-running failure\noutput:\n%s", string(out))
	}
	return string(out)
}
