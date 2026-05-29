package test

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/weill-labs/amux/internal/testenv"
)

func hermeticMainEnv() []string {
	return testenv.HermeticMainEnv()
}

func newHermeticAmuxCommand(tb testing.TB, args ...string) *exec.Cmd {
	if tb != nil {
		tb.Helper()
	}
	return newHermeticAmuxCommandContext(tb, context.Background(), args...)
}

func newHermeticAmuxCommandContext(tb testing.TB, ctx context.Context, args ...string) *exec.Cmd {
	if tb != nil {
		tb.Helper()
	}
	return newHermeticAmuxCommandWithBinContext(tb, ctx, amuxBin, args...)
}

func newHermeticAmuxCommandWithBinContext(tb testing.TB, ctx context.Context, binPath string, args ...string) *exec.Cmd {
	if tb != nil {
		tb.Helper()
	}
	return testenv.NewCommandContext(ctx, binPath, args...)
}

func repoRoot(tb testing.TB) string {
	tb.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("resolve helper file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}

func repoPath(tb testing.TB, rel string) string {
	tb.Helper()

	return filepath.Join(repoRoot(tb), filepath.FromSlash(rel))
}

func TestSkipAuditDir(t *testing.T) {
	t.Parallel()

	skip := []string{".git", ".orca", "vendor", "testdata", "node_modules"}
	for _, name := range skip {
		if !skipAuditDir(name) {
			t.Errorf("skipAuditDir(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"internal", "test", "render", "client", ".orcas", "orca"} {
		if skipAuditDir(name) {
			t.Errorf("skipAuditDir(%q) = true, want false", name)
		}
	}
}

// skipAuditDir reports whether a directory (by base name) should be skipped by
// repo-tree audit walks. It excludes VCS metadata, vendored deps, test
// fixtures, and orca clone-pool worktrees (.orca) whose duplicated *_test.go
// copies would otherwise trip the audits with phantom findings. See LAB-1971.
func skipAuditDir(name string) bool {
	switch name {
	case ".git", ".orca", "vendor", "testdata", "node_modules":
		return true
	default:
		return false
	}
}

func testLogDir(home string) string {
	return filepath.Join(home, ".local", "state", "amux", "logs")
}
