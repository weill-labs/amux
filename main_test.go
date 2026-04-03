package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMainSubprocessHelper(t *testing.T) {
	if os.Getenv("AMUX_ROOT_HELPER") != "1" {
		return
	}

	args := os.Args[1:]
	for i, arg := range args {
		if arg == "--" {
			os.Args = append([]string{"amux"}, args[i+1:]...)
			main()
		}
	}
	t.Fatal("missing -- separator")
}

// Not parallel: mutates os.Args, os.Stdout, and BuildCommit.
func TestRunUsesBuildCommitForVersionHash(t *testing.T) {
	oldArgs := os.Args
	oldStdout := os.Stdout
	oldBuildCommit := BuildCommit
	t.Cleanup(func() {
		os.Args = oldArgs
		os.Stdout = oldStdout
		BuildCommit = oldBuildCommit
	})

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	BuildCommit = "abc1234"
	os.Args = []string{"amux", "version", "--hash"}

	exitCode := run()

	if err := w.Close(); err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout pipe: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close read pipe: %v", err)
	}

	if exitCode != 0 {
		t.Fatalf("run() exit = %d, want 0", exitCode)
	}
	if got := strings.TrimSpace(string(out)); got != "abc1234" {
		t.Fatalf("version hash output = %q, want %q", got, "abc1234")
	}
}

func TestMainHelpViaSubprocess(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestMainSubprocessHelper", "--", "help")
	cmd.Env = rootMainTestEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess main(help): %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Usage:") {
		t.Fatalf("help output missing usage:\n%s", out)
	}
}

func rootMainTestEnv() []string {
	env := make([]string, 0, 8)
	for _, key := range []string{
		"HOME",
		"LANG",
		"LC_ALL",
		"PATH",
		"SHELL",
		"TMPDIR",
		"USER",
	} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return append(env,
		"AMUX_ROOT_HELPER=1",
		"TERM=xterm-256color",
	)
}
