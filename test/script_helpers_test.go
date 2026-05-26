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

func testLogDir(home string) string {
	return filepath.Join(home, ".local", "state", "amux", "logs")
}
